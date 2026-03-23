package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type fakeServeProcess struct {
	waitCh    chan error
	stopErr   error
	killErr   error
	stopCount int
	killCount int
}

func newFakeServeProcess() *fakeServeProcess {
	return &fakeServeProcess{waitCh: make(chan error, 1)}
}

func (p *fakeServeProcess) Wait() error {
	return <-p.waitCh
}

func (p *fakeServeProcess) Stop() error {
	p.stopCount++
	select {
	case p.waitCh <- nil:
	default:
	}
	return p.stopErr
}

func (p *fakeServeProcess) Kill() error {
	p.killCount++
	select {
	case p.waitCh <- nil:
	default:
	}
	return p.killErr
}

// hangingFakeServeProcess simulates a process that ignores Stop() and only
// exits when Kill() is called. Used to test the stop-timeout fallback path.
type hangingFakeServeProcess struct {
	waitCh    chan error
	stopCount int
	killCount int
}

func newHangingFakeServeProcess() *hangingFakeServeProcess {
	return &hangingFakeServeProcess{waitCh: make(chan error, 1)}
}

func (p *hangingFakeServeProcess) Wait() error {
	return <-p.waitCh
}

func (p *hangingFakeServeProcess) Stop() error {
	p.stopCount++
	// deliberately does NOT send to waitCh — process keeps running
	return nil
}

func (p *hangingFakeServeProcess) Kill() error {
	p.killCount++
	select {
	case p.waitCh <- nil:
	default:
	}
	return nil
}

type recordedServeRequest struct {
	Method string
	Path   string
	Body   string
}

type serveHealthResponse struct {
	status int
	body   string
}

type serveTestAPI struct {
	server   *http.Server
	listener net.Listener

	mu              sync.Mutex
	requests        []recordedServeRequest
	sessionID       string
	healthBody      string
	healthResponses []serveHealthResponse
	messageStatus   int
	messageBody     string
	abortStatus     int
	messageNotify   chan struct{}
}

func newServeTestAPI(t *testing.T) *serveTestAPI {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test api: %v", err)
	}

	api := &serveTestAPI{
		listener:    listener,
		sessionID:   "session-1",
		healthBody:  `{"healthy":true,"version":"test"}`,
		messageBody: `{"info":{"id":"message-1"},"parts":[]}`,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/global/health", api.handleHealth)
	mux.HandleFunc("/session", api.handleSession)
	mux.HandleFunc("/session/", api.handleSessionByID)
	mux.HandleFunc("/instance/dispose", api.handleDispose)

	api.server = &http.Server{Handler: mux}
	go func() {
		_ = api.server.Serve(listener)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = api.server.Shutdown(ctx)
	})
	return api
}

func (api *serveTestAPI) port(t *testing.T) int {
	t.Helper()
	_, rawPort, err := net.SplitHostPort(api.listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}
	return port
}

func (api *serveTestAPI) Requests() []recordedServeRequest {
	api.mu.Lock()
	defer api.mu.Unlock()
	out := make([]recordedServeRequest, len(api.requests))
	copy(out, api.requests)
	return out
}

func (api *serveTestAPI) record(r *http.Request, body []byte) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.requests = append(api.requests, recordedServeRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Body:   string(body),
	})
}

func (api *serveTestAPI) handleHealth(w http.ResponseWriter, r *http.Request) {
	api.record(r, nil)
	api.mu.Lock()
	response := serveHealthResponse{
		status: http.StatusOK,
		body:   api.healthBody,
	}
	if len(api.healthResponses) > 0 {
		response = api.healthResponses[0]
		api.healthResponses = api.healthResponses[1:]
		if response.status == 0 {
			response.status = http.StatusOK
		}
		if strings.TrimSpace(response.body) == "" {
			response.body = api.healthBody
		}
	}
	api.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.status)
	_, _ = io.WriteString(w, response.body)
}

func (api *serveTestAPI) handleSession(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	api.record(r, body)
	switch r.Method {
	case http.MethodPost:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": api.sessionID})
	default:
		http.NotFound(w, r)
	}
}

func (api *serveTestAPI) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	api.record(r, body)
	switch r.Method {
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `true`)
	case http.MethodPost:
		if strings.HasSuffix(r.URL.Path, "/message") {
			api.mu.Lock()
			status := api.messageStatus
			body := api.messageBody
			notify := api.messageNotify
			api.mu.Unlock()
			if status == 0 {
				status = http.StatusOK
			}
			if strings.TrimSpace(body) == "" {
				body = `{"info":{"id":"message-1"},"parts":[]}`
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			if notify != nil {
				select {
				case notify <- struct{}{}:
				default:
				}
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/abort") {
			api.mu.Lock()
			status := api.abortStatus
			api.mu.Unlock()
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `true`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `true`)
	default:
		http.NotFound(w, r)
	}
}

func (api *serveTestAPI) handleDispose(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	api.record(r, body)
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `true`)
}

func TestTaskSessionRuntimeWaitReadyStartsServeOnLoopbackAndPollsHealthUntilReady(t *testing.T) {
	api := newServeTestAPI(t)
	api.healthResponses = []serveHealthResponse{
		{status: http.StatusServiceUnavailable, body: `{"healthy":false}`},
		{status: http.StatusOK, body: `{"healthy":true,"version":"test"}`},
	}
	proc := newFakeServeProcess()

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("opencode")
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		startedSpec = spec
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-1",
		RepoRoot:     t.TempDir(),
		LogPath:      filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-1.jsonl"),
		ReadyTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready second call: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	appSession, ok := session.(*ServeTaskSession)
	if !ok {
		t.Fatalf("expected ServeTaskSession, got %T", session)
	}
	if appSession.ID() != "task-1" {
		t.Fatalf("expected task session id task-1, got %q", appSession.ID())
	}
	if appSession.currentSessionID() != "session-1" {
		t.Fatalf("expected readiness to create opencode session session-1, got %q", appSession.currentSessionID())
	}
	if startedSpec.Binary != "opencode" {
		t.Fatalf("expected opencode binary, got %q", startedSpec.Binary)
	}
	if startedSpec.Dir == "" {
		t.Fatalf("expected repo root in serve command spec")
	}

	expectedArgs := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(api.port(t))}
	if len(startedSpec.Args) != len(expectedArgs) {
		t.Fatalf("expected args %#v, got %#v", expectedArgs, startedSpec.Args)
	}
	for i, want := range expectedArgs {
		if startedSpec.Args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, startedSpec.Args[i])
		}
	}

	requests := api.Requests()
	if len(requests) != 3 {
		t.Fatalf("expected exactly two health polls and one session creation request, got %#v", requests)
	}
	for i, request := range requests[:2] {
		if request.Method != http.MethodGet || request.Path != "/global/health" {
			t.Fatalf("expected health polling request at %d, got %#v", i, request)
		}
	}
	createRequest := requests[2]
	if createRequest.Method != http.MethodPost || createRequest.Path != "/session" {
		t.Fatalf("expected session creation request, got %#v", createRequest)
	}
	if !strings.Contains(createRequest.Body, `"title":"task-1"`) {
		t.Fatalf("expected session creation body to include task title, got %q", createRequest.Body)
	}
}

func TestTaskSessionRuntimeDefaultCommandUsesBaseServeBuilder(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	originalBuildServeCommand := buildServeCommand
	t.Cleanup(func() {
		buildServeCommand = originalBuildServeCommand
	})

	builderCalls := 0
	buildServeCommand = func(binary string) []string {
		builderCalls++
		if binary != "/tmp/custom-opencode" {
			t.Fatalf("expected runtime binary to flow into base serve builder, got %q", binary)
		}
		return []string{binary, "serve", "--hostname", "127.0.0.1"}
	}

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		startedSpec = spec
		return proc, nil
	})
	runtime.allocatePort = func(hostname string) (int, error) {
		if hostname != defaultServeHostname {
			t.Fatalf("expected loopback host allocation outside builder, got %q", hostname)
		}
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-builder",
		RepoRoot:     t.TempDir(),
		LogPath:      filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-builder.jsonl"),
		ReadyTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	if builderCalls != 1 {
		t.Fatalf("expected one base serve builder call, got %d", builderCalls)
	}
	if startedSpec.Binary != "/tmp/custom-opencode" {
		t.Fatalf("expected builder-selected binary, got %q", startedSpec.Binary)
	}

	expectedArgs := []string{"serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(api.port(t))}
	if len(startedSpec.Args) != len(expectedArgs) {
		t.Fatalf("expected args %#v, got %#v", expectedArgs, startedSpec.Args)
	}
	for i, want := range expectedArgs {
		if startedSpec.Args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, startedSpec.Args[i])
		}
	}
}

func TestTaskSessionRuntimeDefaultCommandTrimsBuilderPrefixArgs(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	originalBuildServeCommand := buildServeCommand
	t.Cleanup(func() {
		buildServeCommand = originalBuildServeCommand
	})

	buildServeCommand = func(string) []string {
		return []string{" env ", " OPENCODE_TRACE=1 ", " "}
	}

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		startedSpec = spec
		return proc, nil
	})
	runtime.allocatePort = func(hostname string) (int, error) {
		if hostname != defaultServeHostname {
			t.Fatalf("expected loopback host allocation outside builder, got %q", hostname)
		}
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-builder-trim",
		RepoRoot:     t.TempDir(),
		LogPath:      filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-builder-trim.jsonl"),
		ReadyTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	if startedSpec.Binary != "env" {
		t.Fatalf("expected trimmed prefix binary, got %q", startedSpec.Binary)
	}

	expectedArgs := []string{"OPENCODE_TRACE=1", "/tmp/custom-opencode", "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(api.port(t))}
	if len(startedSpec.Args) != len(expectedArgs) {
		t.Fatalf("expected args %#v, got %#v", expectedArgs, startedSpec.Args)
	}
	for i, want := range expectedArgs {
		if startedSpec.Args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, startedSpec.Args[i])
		}
	}
}

func TestTaskSessionRuntimeStartPreparedServeProcessUsesConfiguredStarterPrefix(t *testing.T) {
	proc := newFakeServeProcess()

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode", "env", "OPENCODE_TRACE=1")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		startedSpec = spec
		return proc, nil
	})

	startedProc, err := runtime.startPreparedServeProcess(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-configured-starter",
		RepoRoot: t.TempDir(),
	}, io.Discard, io.Discard, defaultServeHostname, 43123)
	if err != nil {
		t.Fatalf("start prepared serve process: %v", err)
	}
	if startedProc != proc {
		t.Fatalf("expected helper to return starter process handle")
	}

	if startedSpec.Binary != "env" {
		t.Fatalf("expected configured starter prefix binary, got %q", startedSpec.Binary)
	}
	expectedArgs := []string{"OPENCODE_TRACE=1", "/tmp/custom-opencode", "serve", "--hostname", "127.0.0.1", "--port", "43123"}
	if len(startedSpec.Args) != len(expectedArgs) {
		t.Fatalf("expected args %#v, got %#v", expectedArgs, startedSpec.Args)
	}
	for i, want := range expectedArgs {
		if startedSpec.Args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, startedSpec.Args[i])
		}
	}
}

func TestTaskSessionRuntimeStartPreparedServeProcessUsesRequestCommandStarterPrefix(t *testing.T) {
	proc := newFakeServeProcess()

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		startedSpec = spec
		return proc, nil
	})

	startedProc, err := runtime.startPreparedServeProcess(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-request-starter",
		RepoRoot: t.TempDir(),
		Command:  []string{"env", "OPENCODE_TRACE=1"},
	}, io.Discard, io.Discard, defaultServeHostname, 43123)
	if err != nil {
		t.Fatalf("start prepared serve process: %v", err)
	}
	if startedProc != proc {
		t.Fatalf("expected helper to return starter process handle")
	}

	if startedSpec.Binary != "env" {
		t.Fatalf("expected request command starter prefix binary, got %q", startedSpec.Binary)
	}
	expectedArgs := []string{"OPENCODE_TRACE=1", "/tmp/custom-opencode", "serve", "--hostname", "127.0.0.1", "--port", "43123"}
	if len(startedSpec.Args) != len(expectedArgs) {
		t.Fatalf("expected args %#v, got %#v", expectedArgs, startedSpec.Args)
	}
	for i, want := range expectedArgs {
		if startedSpec.Args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, startedSpec.Args[i])
		}
	}
}

func TestTaskSessionRuntimeStartPreparedServeProcessRejectsNilProcess(t *testing.T) {
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode")
	runtime.starter = serveProcessStarterFunc(func(context.Context, ServeCommandSpec) (serveProcess, error) {
		return nil, nil
	})

	startedProc, err := runtime.startPreparedServeProcess(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-nil-process",
		RepoRoot: t.TempDir(),
	}, io.Discard, io.Discard, defaultServeHostname, 43123)
	if err == nil {
		t.Fatal("expected nil process error")
	}
	if startedProc != nil {
		t.Fatalf("expected nil process handle, got %#v", startedProc)
	}
	if !strings.Contains(err.Error(), "nil process") {
		t.Fatalf("expected nil process error, got %v", err)
	}
}

func TestTaskSessionRuntimeNewInitialServeTaskSessionUsesResolvedURLsAndStartedProcess(t *testing.T) {
	proc := newFakeServeProcess()
	waitErr := errors.New("serve exited")

	client := &http.Client{}
	runtime := NewTaskSessionRuntime("opencode")
	runtime.httpClient = client
	runtime.healthCheckInterval = 250 * time.Millisecond

	session := runtime.newInitialServeTaskSession(contracts.TaskSessionStartRequest{
		TaskID:       "task-initial-shell",
		ReadyTimeout: time.Second,
		StopTimeout:  1500 * time.Millisecond,
	}, proc, nil, nil, "0.0.0.0", 43123)

	if session == nil {
		t.Fatal("expected initial serve task session")
	}
	if session.proc != proc {
		t.Fatalf("expected session to retain started process handle")
	}
	if session.client != client {
		t.Fatalf("expected session to retain runtime http client")
	}
	if session.ID() != "task-initial-shell" {
		t.Fatalf("expected task session id, got %q", session.ID())
	}
	if session.baseURL != "http://localhost:43123" {
		t.Fatalf("expected resolved base url, got %q", session.baseURL)
	}
	if session.healthURL != "http://localhost:43123/global/health" {
		t.Fatalf("expected resolved health url, got %q", session.healthURL)
	}
	if session.sessionURL != "http://localhost:43123/session" {
		t.Fatalf("expected resolved session url, got %q", session.sessionURL)
	}
	if session.disposeURL != "http://localhost:43123/instance/dispose" {
		t.Fatalf("expected resolved dispose url, got %q", session.disposeURL)
	}
	if session.healthCheckInterval != 250*time.Millisecond {
		t.Fatalf("expected runtime health check interval, got %s", session.healthCheckInterval)
	}
	if session.readyTimeout != time.Second {
		t.Fatalf("expected ready timeout, got %s", session.readyTimeout)
	}
	if session.stopTimeout != 1500*time.Millisecond {
		t.Fatalf("expected stop timeout, got %s", session.stopTimeout)
	}
	if session.currentSessionID() != "" {
		t.Fatalf("expected initial serve task session shell without created session id, got %q", session.currentSessionID())
	}

	proc.waitCh <- waitErr

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.waitWithContext(ctx); !errors.Is(err, waitErr) {
		t.Fatalf("expected process wait error %v, got %v", waitErr, err)
	}
}

func TestServeTaskSessionExecuteCreatesSessionAndSubmitsOnePromptMessage(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-execute",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-execute.jsonl"),
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	appSession, ok := session.(*ServeTaskSession)
	if !ok {
		t.Fatalf("expected ServeTaskSession, got %T", session)
	}

	err = appSession.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
		Prompt: "implement the task",
	})
	if err != nil {
		t.Fatalf("execute session: %v", err)
	}

	if appSession.currentSessionID() != "session-1" {
		t.Fatalf("expected created session id to be stored, got %q", appSession.currentSessionID())
	}

	requests := api.Requests()
	foundCreate := false
	foundMessage := false
	for _, request := range requests {
		if request.Method == http.MethodPost && request.Path == "/session" {
			foundCreate = true
			if strings.TrimSpace(request.Body) != `{"title":"task-execute"}` {
				t.Fatalf("unexpected create session body %q", request.Body)
			}
		}
		if request.Method == http.MethodPost && request.Path == "/session/session-1/message" {
			foundMessage = true
			if strings.TrimSpace(request.Body) != `{"parts":[{"type":"text","text":"implement the task"}]}` {
				t.Fatalf("unexpected message submit body %q", request.Body)
			}
		}
	}
	if !foundCreate {
		t.Fatalf("expected create session request, got %#v", requests)
	}
	if !foundMessage {
		t.Fatalf("expected message submit request, got %#v", requests)
	}
}

func TestServeTaskSessionExecuteReusesExistingSessionForOnePromptMessage(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-existing-session",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-existing-session.jsonl"),
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	appSession, ok := session.(*ServeTaskSession)
	if !ok {
		t.Fatalf("expected ServeTaskSession, got %T", session)
	}
	appSession.setSessionID("session-existing")

	countBeforeExecute := len(api.Requests())
	err = appSession.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
		Prompt: "continue the task",
	})
	if err != nil {
		t.Fatalf("execute session: %v", err)
	}

	requests := api.Requests()
	newRequests := requests[countBeforeExecute:]
	for _, request := range newRequests {
		if request.Method == http.MethodPost && request.Path == "/session" {
			t.Fatalf("did not expect create session request from Execute, got %#v", newRequests)
		}
	}

	foundMessage := false
	for _, request := range newRequests {
		if request.Method == http.MethodPost && request.Path == "/session/session-existing/message" {
			foundMessage = true
			if strings.TrimSpace(request.Body) != `{"parts":[{"type":"text","text":"continue the task"}]}` {
				t.Fatalf("unexpected message submit body %q", request.Body)
			}
		}
	}
	if !foundMessage {
		t.Fatalf("expected message submit request, got %#v", requests)
	}
}

func TestServeTaskSessionTeardownDeletesEphemeralSessionAndStopsProcess(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-2",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-2.jsonl"),
		StopTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	appSession, ok := session.(*ServeTaskSession)
	if !ok {
		t.Fatalf("expected ServeTaskSession, got %T", session)
	}
	appSession.stateMu.Lock()
	appSession.sessionID = "session-1"
	appSession.stateMu.Unlock()

	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "finished"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}

	requests := api.Requests()
	if len(requests) < 3 {
		t.Fatalf("expected health, delete and dispose requests, got %#v", requests)
	}
	foundDelete := false
	foundDispose := false
	for _, request := range requests {
		if request.Method == http.MethodDelete && request.Path == "/session/session-1" {
			foundDelete = true
		}
		if request.Method == http.MethodPost && request.Path == "/instance/dispose" {
			foundDispose = true
		}
	}
	if !foundDelete {
		t.Fatalf("expected session delete request, got %#v", requests)
	}
	if !foundDispose {
		t.Fatalf("expected instance dispose request, got %#v", requests)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected one graceful stop, got %d", proc.stopCount)
	}
	if proc.killCount != 0 {
		t.Fatalf("did not expect forced kill, got %d", proc.killCount)
	}
}

func TestServeTaskSessionExecutePostsPromptToExistingSessionMessageEndpoint(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-execute",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-execute.jsonl"),
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}

	if err := session.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
		Prompt: "ship the fix",
	}); err != nil {
		t.Fatalf("execute session: %v", err)
	}

	requests := api.Requests()
	found := false
	for _, request := range requests {
		if request.Method != http.MethodPost || request.Path != "/session/session-1/message" {
			continue
		}
		found = true

		var payload struct {
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		}
		if err := json.Unmarshal([]byte(request.Body), &payload); err != nil {
			t.Fatalf("decode execute body: %v", err)
		}
		if len(payload.Parts) != 1 {
			t.Fatalf("expected one prompt part, got %#v", payload.Parts)
		}
		if payload.Parts[0].Type != "text" {
			t.Fatalf("expected text prompt part, got %#v", payload.Parts[0])
		}
		if payload.Parts[0].Text != "ship the fix" {
			t.Fatalf("expected prompt text in execute body, got %#v", payload.Parts[0])
		}
	}
	if !found {
		t.Fatalf("expected execute request to /session/session-1/message, got %#v", requests)
	}
}

func TestServeTaskSessionExecuteReturnsHTTPErrorDetails(t *testing.T) {
	api := newServeTestAPI(t)
	api.messageStatus = http.StatusBadGateway
	api.messageBody = `{"error":"upstream failure"}`
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-execute-error",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-execute-error.jsonl"),
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}

	err = session.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
		Prompt: "ship the fix",
	})
	if err == nil {
		t.Fatal("expected execute error")
	}
	if !strings.Contains(err.Error(), "submit session message returned 502") {
		t.Fatalf("expected execute status in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "upstream failure") {
		t.Fatalf("expected execute response body in error, got %v", err)
	}
}

func TestServeTaskSessionWaitReadyFailsWhenProcessExitsBeforeHealth(t *testing.T) {
	proc := newFakeServeProcess()
	proc.waitCh <- errors.New("boom")

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return 1, nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-3",
		RepoRoot:     t.TempDir(),
		LogPath:      filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-3.jsonl"),
		ReadyTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	err = session.WaitReady(context.Background())
	if err == nil {
		t.Fatalf("expected readiness failure")
	}
	if !strings.Contains(err.Error(), "before readiness") {
		t.Fatalf("expected early exit readiness error, got %v", err)
	}
}

func TestServeTaskSessionWaitReadyTimeoutIncludesHealthContext(t *testing.T) {
	api := newServeTestAPI(t)
	api.healthResponses = make([]serveHealthResponse, 32)
	for i := range api.healthResponses {
		api.healthResponses[i] = serveHealthResponse{status: http.StatusServiceUnavailable, body: `{"healthy":false}`}
	}
	proc := newFakeServeProcess()

	repoRoot := t.TempDir()
	logPath := filepath.Join(repoRoot, "runner-logs", "opencode", "task-timeout.jsonl")
	runtime := NewTaskSessionRuntime("opencode")
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-timeout",
		RepoRoot:     repoRoot,
		LogPath:      logPath,
		ReadyTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	err = session.WaitReady(context.Background())
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	if !strings.Contains(err.Error(), "timed out waiting for opencode serve readiness") {
		t.Fatalf("expected readiness timeout message, got %v", err)
	}
	if !strings.Contains(err.Error(), "/global/health") {
		t.Fatalf("expected health endpoint in readiness timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "health endpoint returned 503") {
		t.Fatalf("expected last health failure in readiness timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)) {
		t.Fatalf("expected stderr log path in readiness timeout, got %v", err)
	}
}

func TestServeTaskSessionCancelSendsAbortToActiveSession(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-cancel",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-cancel.jsonl"),
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Reason: "user cancelled"}); err != nil {
		t.Fatalf("cancel session: %v", err)
	}

	requests := api.Requests()
	foundAbort := false
	for _, request := range requests {
		if request.Method == http.MethodPost && request.Path == "/session/session-1/abort" {
			foundAbort = true
		}
	}
	if !foundAbort {
		t.Fatalf("expected abort request to /session/session-1/abort, got %#v", requests)
	}
}

func TestServeTaskSessionCancelReturnsErrorWhenAbortEndpointFails(t *testing.T) {
	api := newServeTestAPI(t)
	api.abortStatus = http.StatusInternalServerError
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-cancel-error",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-cancel-error.jsonl"),
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "test cleanup", Force: true})
	})

	err = session.Cancel(context.Background(), contracts.TaskSessionCancellation{Reason: "user cancelled"})
	if err == nil {
		t.Fatal("expected cancel error when abort endpoint fails")
	}
	if !strings.Contains(err.Error(), "abort session returned 500") {
		t.Fatalf("expected abort status in error, got %v", err)
	}
}

func TestServeTaskSessionCancelForceKillsProcessAndCleansUpSession(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-cancel-force",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-cancel-force.jsonl"),
		StopTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}

	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Reason: "user cancelled", Force: true}); err != nil {
		t.Fatalf("force cancel session: %v", err)
	}

	requests := api.Requests()
	foundDelete := false
	foundDispose := false
	for _, request := range requests {
		if request.Method == http.MethodDelete && request.Path == "/session/session-1" {
			foundDelete = true
		}
		if request.Method == http.MethodPost && request.Path == "/instance/dispose" {
			foundDispose = true
		}
	}
	if !foundDelete {
		t.Fatalf("expected session delete request during force cancel, got %#v", requests)
	}
	if !foundDispose {
		t.Fatalf("expected instance dispose request during force cancel, got %#v", requests)
	}
	if proc.killCount != 1 {
		t.Fatalf("expected one forced kill during force cancel, got %d", proc.killCount)
	}
	if proc.stopCount != 0 {
		t.Fatalf("did not expect graceful stop during force cancel, got %d", proc.stopCount)
	}
}

func TestServeTaskSessionWaitReadyIncludesStderrDetailsWhenServeExitsBeforeReadiness(t *testing.T) {
	proc := newFakeServeProcess()
	proc.waitCh <- errors.New("exit status 1")

	repoRoot := t.TempDir()
	logPath := filepath.Join(repoRoot, "runner-logs", "opencode", "task-bind.jsonl")
	runtime := NewTaskSessionRuntime("opencode")
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return 43123, nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-bind",
		RepoRoot:     repoRoot,
		LogPath:      logPath,
		ReadyTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	appSession, ok := session.(*ServeTaskSession)
	if !ok {
		t.Fatalf("expected ServeTaskSession, got %T", session)
	}
	if _, err := io.WriteString(appSession.stderrFile, "listen tcp 127.0.0.1:43123: bind: address already in use\n"); err != nil {
		t.Fatalf("seed stderr log: %v", err)
	}
	if err := appSession.stderrFile.Sync(); err != nil {
		t.Fatalf("sync stderr log: %v", err)
	}

	err = session.WaitReady(context.Background())
	if err == nil {
		t.Fatal("expected readiness failure")
	}
	if !strings.Contains(err.Error(), "before readiness") {
		t.Fatalf("expected early exit readiness error, got %v", err)
	}
	if !strings.Contains(err.Error(), "bind: address already in use") {
		t.Fatalf("expected stderr bind details in readiness error, got %v", err)
	}
	if !strings.Contains(err.Error(), contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)) {
		t.Fatalf("expected stderr log path in readiness error, got %v", err)
	}
}

func TestCreateSessionPostsTitleBodyAndReturnsSessionID(t *testing.T) {
	var recordedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/session" {
			http.NotFound(w, r)
			return
		}
		recordedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses-focused"})
	}))
	defer server.Close()

	session := &ServeTaskSession{
		client:     server.Client(),
		sessionURL: server.URL + "/session",
		taskTitle:  "focused-title",
	}

	id, err := session.createSession(context.Background())
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if id != "ses-focused" {
		t.Fatalf("expected ses-focused, got %q", id)
	}
	if strings.TrimSpace(string(recordedBody)) != `{"title":"focused-title"}` {
		t.Fatalf("unexpected request body: %q", string(recordedBody))
	}
}

func TestCreateSessionReturnsErrorForNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()

	session := &ServeTaskSession{
		client:     server.Client(),
		sessionURL: server.URL + "/session",
	}

	_, err := session.createSession(context.Background())
	if err == nil {
		t.Fatalf("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected error to mention status 500, got %v", err)
	}
}

func TestCreateSessionReturnsErrorWhenResponseIDIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": ""})
	}))
	defer server.Close()

	session := &ServeTaskSession{
		client:     server.Client(),
		sessionURL: server.URL + "/session",
	}

	_, err := session.createSession(context.Background())
	if err == nil {
		t.Fatalf("expected error for empty session ID")
	}
	if !strings.Contains(err.Error(), "missing id") {
		t.Fatalf("expected missing id error, got %v", err)
	}
}

func TestSubmitPromptMessagePostsPartsPayloadToSessionMessageEndpoint(t *testing.T) {
	var recordedPath string
	var recordedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedPath = r.URL.Path
		recordedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	session := &ServeTaskSession{
		client:     server.Client(),
		sessionURL: server.URL + "/session",
	}

	err := session.submitPromptMessage(context.Background(), "ses-abc", "ship it")
	if err != nil {
		t.Fatalf("submitPromptMessage: %v", err)
	}
	if recordedPath != "/session/ses-abc/message" {
		t.Fatalf("expected /session/ses-abc/message, got %q", recordedPath)
	}
	expected := `{"parts":[{"type":"text","text":"ship it"}]}`
	if strings.TrimSpace(string(recordedBody)) != expected {
		t.Fatalf("unexpected body: %q", string(recordedBody))
	}
}

func TestSubmitPromptMessageReturnsErrorWithBodyOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":"prompt too long"}`)
	}))
	defer server.Close()

	session := &ServeTaskSession{
		client:     server.Client(),
		sessionURL: server.URL + "/session",
	}

	err := session.submitPromptMessage(context.Background(), "ses-xyz", "some prompt")
	if err == nil {
		t.Fatalf("expected error for 422 response")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Fatalf("expected error to mention status 422, got %v", err)
	}
	if !strings.Contains(err.Error(), "prompt too long") {
		t.Fatalf("expected error to include response body, got %v", err)
	}
}

// TestServeTaskSessionCancelReturnsNilWhenNoSessionEstablished verifies that
// Cancel with force=false returns nil immediately when no session ID has been
// established yet, without sending any HTTP request.
func TestServeTaskSessionCancelReturnsNilWhenNoSessionEstablished(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	waitDone := make(chan struct{})
	close(waitDone)
	session := &ServeTaskSession{
		client:     srv.Client(),
		sessionURL: srv.URL + "/session",
		waitDone:   waitDone,
		proc:       newFakeServeProcess(),
	}
	// sessionID is "" (zero value) — no session established

	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Reason: "no session yet"}); err != nil {
		t.Fatalf("expected nil from Cancel when no session established, got %v", err)
	}
	if requestCount != 0 {
		t.Fatalf("expected no HTTP requests when no session established, got %d", requestCount)
	}
}

// TestServeTaskSessionTeardownSkipsSessionDeleteWhenNoSessionEstablished verifies
// that Teardown omits the DELETE /session/{id} request when no session ID was ever
// set, while still calling /instance/dispose and stopping the process.
func TestServeTaskSessionTeardownSkipsSessionDeleteWhenNoSessionEstablished(t *testing.T) {
	var deleteCalled bool
	var disposeCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/session/") {
			deleteCalled = true
		}
		if r.Method == http.MethodPost && r.URL.Path == "/instance/dispose" {
			disposeCalled = true
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `true`)
	}))
	defer srv.Close()

	proc := newFakeServeProcess()
	session := &ServeTaskSession{
		client:     srv.Client(),
		sessionURL: srv.URL + "/session",
		disposeURL: srv.URL + "/instance/dispose",
		proc:       proc,
		waitDone:   make(chan struct{}),
	}
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()

	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "no session"}); err != nil {
		t.Fatalf("unexpected teardown error: %v", err)
	}
	if deleteCalled {
		t.Fatal("expected DELETE session to be skipped when no session ID")
	}
	if !disposeCalled {
		t.Fatal("expected /instance/dispose to be called even without session ID")
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected one graceful stop, got %d", proc.stopCount)
	}
}

// TestServeTaskSessionTeardownIsIdempotent verifies that calling Teardown a
// second time returns the same result as the first call without making additional
// HTTP requests or additional process signals.
func TestServeTaskSessionTeardownIsIdempotent(t *testing.T) {
	disposeCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/instance/dispose" {
			disposeCount++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `true`)
	}))
	defer srv.Close()

	proc := newFakeServeProcess()
	session := &ServeTaskSession{
		client:     srv.Client(),
		sessionURL: srv.URL + "/session",
		disposeURL: srv.URL + "/instance/dispose",
		proc:       proc,
		waitDone:   make(chan struct{}),
	}
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()

	err1 := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "first call"})
	err2 := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "second call"})

	if err1 != err2 {
		t.Fatalf("expected idempotent teardown: first %v, second %v", err1, err2)
	}
	if disposeCount != 1 {
		t.Fatalf("expected dispose called exactly once, got %d", disposeCount)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected stop called exactly once, got %d", proc.stopCount)
	}
}

// TestServeTaskSessionTeardownPropagatesDisposeInstanceError verifies that when
// the /instance/dispose endpoint returns an error status, Teardown propagates it.
func TestServeTaskSessionTeardownPropagatesDisposeInstanceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/instance/dispose" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"dispose failed"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `true`)
	}))
	defer srv.Close()

	proc := newFakeServeProcess()
	session := &ServeTaskSession{
		client:     srv.Client(),
		sessionURL: srv.URL + "/session",
		disposeURL: srv.URL + "/instance/dispose",
		proc:       proc,
		waitDone:   make(chan struct{}),
	}
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()

	err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "dispose error"})
	if err == nil {
		t.Fatal("expected error when dispose instance fails")
	}
	if !strings.Contains(err.Error(), "dispose instance returned 500") {
		t.Fatalf("expected dispose error in teardown error, got %v", err)
	}
}

// TestServeTaskSessionTeardownFallsBackToKillWhenStopTimesOut verifies that when
// the process does not exit within stopTimeout after receiving Stop(), the teardown
// falls back to Kill() to force termination.
func TestServeTaskSessionTeardownFallsBackToKillWhenStopTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `true`)
	}))
	defer srv.Close()

	proc := newHangingFakeServeProcess()
	session := &ServeTaskSession{
		client:      srv.Client(),
		sessionURL:  srv.URL + "/session",
		disposeURL:  srv.URL + "/instance/dispose",
		proc:        proc,
		waitDone:    make(chan struct{}),
		stopTimeout: 10 * time.Millisecond,
	}
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()

	// Teardown may return an error (DeadlineExceeded) because the graceful stop
	// timed out; that is expected. What matters is that Kill was called.
	_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "kill fallback"})

	if proc.stopCount != 1 {
		t.Fatalf("expected Stop to be called once, got %d", proc.stopCount)
	}
	if proc.killCount != 1 {
		t.Fatalf("expected Kill to be called as fallback, got %d", proc.killCount)
	}
}
