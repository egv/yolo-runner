package opencode

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// httpRoundTripFunc is an http.RoundTripper backed by a function, used by tests
// that need to intercept individual HTTP requests at the transport level.
type httpRoundTripFunc func(*http.Request) (*http.Response, error)

func (f httpRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// connectionRefusedError simulates a TCP connection-refused network error
// returned when a server is not yet listening on the target address.
type connectionRefusedError struct{}

func (e *connectionRefusedError) Error() string   { return "connection refused" }
func (e *connectionRefusedError) Timeout() bool   { return false }
func (e *connectionRefusedError) Temporary() bool { return true }

// TestTaskSessionRuntimeStartPassesRequestEnvToServeProcess verifies that env
// vars from the start request are flattened into sorted KEY=VALUE pairs and
// forwarded to the OS process through the ServeCommandSpec.
func TestTaskSessionRuntimeStartPassesRequestEnvToServeProcess(t *testing.T) {
	proc := newFakeServeProcess()

	var startedSpec ServeCommandSpec
	runtime := NewTaskSessionRuntime("opencode")
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		startedSpec = spec
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return 43123, nil
	}

	_, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-env",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-env.jsonl"),
		Env: map[string]string{
			"OPENCODE_TOKEN": "secret",
			"DEBUG":          "1",
		},
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	proc.waitCh <- nil

	// Env must be flattened as KEY=VALUE pairs and sorted alphabetically by key.
	wantEnv := []string{"DEBUG=1", "OPENCODE_TOKEN=secret"}
	if len(startedSpec.Env) != len(wantEnv) {
		t.Fatalf("expected env %#v, got %#v", wantEnv, startedSpec.Env)
	}
	for i, want := range wantEnv {
		if startedSpec.Env[i] != want {
			t.Fatalf("expected env[%d] = %q, got %q", i, want, startedSpec.Env[i])
		}
	}
}

// TestTaskSessionRuntimeBuildCommandInjectsHostnameAndPortWhenAbsentFromArgs
// verifies that buildCommand appends --hostname and --port to the args when
// neither flag is already present, so the serve process binds to the
// allocated address.
func TestTaskSessionRuntimeBuildCommandInjectsHostnameAndPortWhenAbsentFromArgs(t *testing.T) {
	runtime := NewTaskSessionRuntime("opencode")

	_, args := runtime.buildCommand(contracts.TaskSessionStartRequest{TaskID: "task-bind-inject"}, defaultServeHostname, 43200)

	hostnameIdx := -1
	portIdx := -1
	for i, arg := range args {
		switch arg {
		case "--hostname":
			hostnameIdx = i
		case "--port":
			portIdx = i
		}
	}

	if hostnameIdx < 0 {
		t.Fatalf("expected --hostname flag in args %#v", args)
	}
	if hostnameIdx+1 >= len(args) || args[hostnameIdx+1] != defaultServeHostname {
		t.Fatalf("expected --hostname %q after flag, got %#v", defaultServeHostname, args)
	}
	if portIdx < 0 {
		t.Fatalf("expected --port flag in args %#v", args)
	}
	if portIdx+1 >= len(args) || args[portIdx+1] != "43200" {
		t.Fatalf("expected --port 43200 after flag, got %#v", args)
	}
}

// TestTaskSessionRuntimeBuildCommandDoesNotDuplicateHostnameOrPortFlags
// verifies that buildCommand does not append --hostname or --port a second
// time when those flags are already present in the runtime args.
func TestTaskSessionRuntimeBuildCommandDoesNotDuplicateHostnameOrPortFlags(t *testing.T) {
	runtime := NewTaskSessionRuntime("opencode", "serve", "--hostname", "127.0.0.1", "--port", "43123")

	_, args := runtime.buildCommand(contracts.TaskSessionStartRequest{TaskID: "task-bind-dup"}, "127.0.0.1", 43123)

	hostnameCount := 0
	portCount := 0
	for _, arg := range args {
		switch arg {
		case "--hostname":
			hostnameCount++
		case "--port":
			portCount++
		}
	}

	if hostnameCount != 1 {
		t.Fatalf("expected exactly one --hostname flag, got %d in %#v", hostnameCount, args)
	}
	if portCount != 1 {
		t.Fatalf("expected exactly one --port flag, got %d in %#v", portCount, args)
	}
}

// TestServeTaskSessionWaitReadyRetriesOnConnectionRefused verifies that
// WaitReady keeps polling the health endpoint even when the server is not
// yet listening (connection refused), eventually succeeding once the process
// binds and starts accepting connections.
func TestServeTaskSessionWaitReadyRetriesOnConnectionRefused(t *testing.T) {
	api := newServeTestAPI(t)
	proc := newFakeServeProcess()

	var healthCalls int64
	client := &http.Client{
		Transport: httpRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/global/health") {
				n := atomic.AddInt64(&healthCalls, 1)
				if n <= 3 {
					// Simulate connection refused for the first three health polls.
					return nil, &connectionRefusedError{}
				}
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	runtime := NewTaskSessionRuntime("opencode")
	runtime.httpClient = client
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:       "task-refused",
		RepoRoot:     t.TempDir(),
		LogPath:      filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-refused.jsonl"),
		ReadyTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true})
	})

	if err := session.WaitReady(context.Background()); err != nil {
		t.Fatalf("expected WaitReady to succeed after retrying connection-refused health polls: %v", err)
	}

	got := atomic.LoadInt64(&healthCalls)
	if got < 4 {
		t.Fatalf("expected at least 4 health calls (3 refused + 1 successful), got %d", got)
	}
}
