package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
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

type recordedServeRequest struct {
	Method string
	Path   string
	Body   string
}

type serveTestAPI struct {
	server   *http.Server
	listener net.Listener

	mu         sync.Mutex
	requests   []recordedServeRequest
	sessionID  string
	healthBody string
}

func newServeTestAPI(t *testing.T) *serveTestAPI {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test api: %v", err)
	}

	api := &serveTestAPI{
		listener:   listener,
		sessionID:  "session-1",
		healthBody: `{"healthy":true,"version":"test"}`,
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
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, api.healthBody)
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
	case http.MethodDelete, http.MethodPost:
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

func TestTaskSessionRuntimeWaitReadyStartsServeOnLoopbackAndCreatesSession(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("opencode")
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
	if appSession.sessionID != "session-1" {
		t.Fatalf("expected created opencode session id, got %q", appSession.sessionID)
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
	if len(requests) < 2 {
		t.Fatalf("expected health and session creation requests, got %#v", requests)
	}
	if requests[0].Method != http.MethodGet || requests[0].Path != "/global/health" {
		t.Fatalf("expected first request to be health check, got %#v", requests[0])
	}
	if requests[1].Method != http.MethodPost || requests[1].Path != "/session" {
		t.Fatalf("expected second request to create session, got %#v", requests[1])
	}
	if !strings.Contains(requests[1].Body, `"title":"task-1"`) {
		t.Fatalf("expected session create body to include task title, got %q", requests[1].Body)
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
		return []string{binary, "serve"}
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

	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "finished"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}

	requests := api.Requests()
	if len(requests) < 4 {
		t.Fatalf("expected health, create, delete and dispose requests, got %#v", requests)
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
