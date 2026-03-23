package opencode

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// TestServeRunnerAdapterEndToEndHappyPath exercises the full opencode serve pipeline
// without any stub layers: ServeRunnerAdapter → TaskSessionRuntime → fake HTTP API.
//
// The test verifies that a Run() call produces RunnerResultCompleted when the
// underlying serve process exits cleanly after the prompt message is submitted.
func TestServeRunnerAdapterEndToEndHappyPath(t *testing.T) {
	api := newServeTestAPI(t)
	api.messageNotify = make(chan struct{}, 1)

	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, _ ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	adapter := &ServeRunnerAdapter{
		runtime: runtime,
	}

	repoRoot := t.TempDir()
	logPath := filepath.Join(repoRoot, "runner-logs", "opencode", "e2e-task.jsonl")

	type runResult struct {
		result contracts.RunnerResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
			TaskID:   "e2e-task",
			RepoRoot: repoRoot,
			Prompt:   "implement the feature",
			Metadata: map[string]string{"log_path": logPath},
		})
		done <- runResult{result: result, err: err}
	}()

	// Wait for the message endpoint to be called, then signal process exit.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	select {
	case <-api.messageNotify:
	case <-ctx.Done():
		t.Fatal("timed out waiting for message to be submitted to opencode serve")
	}

	// Signal clean process exit — this unblocks waitForServeSessionCompletion.
	proc.waitCh <- nil

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("adapter.Run returned unexpected error: %v", r.err)
		}
		if r.result.Status != contracts.RunnerResultCompleted {
			t.Fatalf("expected RunnerResultCompleted, got %s (reason: %q)", r.result.Status, r.result.Reason)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for adapter.Run to return")
	}

	// Verify the full HTTP request sequence was observed.
	requests := api.Requests()
	foundHealth := false
	foundCreate := false
	foundMessage := false
	foundDelete := false
	foundDispose := false
	for _, req := range requests {
		switch {
		case req.Method == http.MethodGet && req.Path == "/global/health":
			foundHealth = true
		case req.Method == http.MethodPost && req.Path == "/session":
			foundCreate = true
		case req.Method == http.MethodPost && req.Path == "/session/session-1/message":
			foundMessage = true
		case req.Method == http.MethodDelete && req.Path == "/session/session-1":
			foundDelete = true
		case req.Method == http.MethodPost && req.Path == "/instance/dispose":
			foundDispose = true
		}
	}
	if !foundHealth {
		t.Errorf("expected GET /global/health in request sequence, got %#v", requests)
	}
	if !foundCreate {
		t.Errorf("expected POST /session in request sequence, got %#v", requests)
	}
	if !foundMessage {
		t.Errorf("expected POST /session/session-1/message in request sequence, got %#v", requests)
	}
	if !foundDelete {
		t.Errorf("expected DELETE /session/session-1 in request sequence, got %#v", requests)
	}
	if !foundDispose {
		t.Errorf("expected POST /instance/dispose in request sequence, got %#v", requests)
	}
}

// TestServeRunnerAdapterEndToEndProcessExitsWithError verifies the force-teardown
// path: when the underlying serve process exits with a non-nil error, the adapter
// must return RunnerResultFailed, kill the process (force teardown), and still
// issue DELETE /session and POST /instance/dispose before exiting.
func TestServeRunnerAdapterEndToEndProcessExitsWithError(t *testing.T) {
	api := newServeTestAPI(t)
	api.messageNotify = make(chan struct{}, 1)

	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, _ ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	adapter := &ServeRunnerAdapter{
		runtime: runtime,
	}

	repoRoot := t.TempDir()
	logPath := filepath.Join(repoRoot, "runner-logs", "opencode", "e2e-fail.jsonl")

	type runResult struct {
		result contracts.RunnerResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
			TaskID:   "e2e-fail",
			RepoRoot: repoRoot,
			Prompt:   "implement the feature",
			Metadata: map[string]string{"log_path": logPath},
		})
		done <- runResult{result: result, err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Wait for the prompt message to be submitted, then simulate a process crash.
	select {
	case <-api.messageNotify:
	case <-ctx.Done():
		t.Fatal("timed out waiting for message to be submitted to opencode serve")
	}

	// Signal process crash — waitForServeSessionCompletion returns this error.
	proc.waitCh <- errors.New("process crashed: exit status 1")

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("adapter.Run returned unexpected error: %v", r.err)
		}
		if r.result.Status != contracts.RunnerResultFailed {
			t.Fatalf("expected RunnerResultFailed after process crash, got %s (reason: %q)", r.result.Status, r.result.Reason)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for adapter.Run to return after process crash")
	}

	// Force teardown must kill the process (not just stop it).
	if proc.killCount == 0 {
		t.Errorf("expected Kill to be called during force teardown after process error, got killCount=%d", proc.killCount)
	}
	if proc.stopCount != 0 {
		t.Errorf("expected Stop NOT to be called during force teardown, got stopCount=%d", proc.stopCount)
	}

	// DELETE /session and POST /instance/dispose must still be issued even on
	// force teardown so that the opencode server can release its resources.
	requests := api.Requests()
	foundDelete := false
	foundDispose := false
	for _, req := range requests {
		switch {
		case req.Method == http.MethodDelete && req.Path == "/session/session-1":
			foundDelete = true
		case req.Method == http.MethodPost && req.Path == "/instance/dispose":
			foundDispose = true
		}
	}
	if !foundDelete {
		t.Errorf("expected DELETE /session/session-1 even on force teardown, got %#v", requests)
	}
	if !foundDispose {
		t.Errorf("expected POST /instance/dispose even on force teardown, got %#v", requests)
	}
}

// TestServeRunnerAdapterEndToEndContextCancelled verifies the graceful-teardown
// path: when the run context is cancelled while waiting for the session to
// complete, the adapter must return RunnerResultFailed, stop the process
// gracefully (Stop, not Kill), and issue DELETE /session and POST /instance/dispose.
func TestServeRunnerAdapterEndToEndContextCancelled(t *testing.T) {
	api := newServeTestAPI(t)
	api.messageNotify = make(chan struct{}, 1)

	proc := newFakeServeProcess()

	runtime := NewTaskSessionRuntime("opencode")
	runtime.healthCheckInterval = 5 * time.Millisecond
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, _ ServeCommandSpec) (serveProcess, error) {
		return proc, nil
	})
	runtime.allocatePort = func(string) (int, error) {
		return api.port(t), nil
	}

	adapter := &ServeRunnerAdapter{
		runtime: runtime,
	}

	repoRoot := t.TempDir()
	logPath := filepath.Join(repoRoot, "runner-logs", "opencode", "e2e-cancel.jsonl")

	runCtx, cancelRun := context.WithCancel(context.Background())

	type runResult struct {
		result contracts.RunnerResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := adapter.Run(runCtx, contracts.RunnerRequest{
			TaskID:   "e2e-cancel",
			RepoRoot: repoRoot,
			Prompt:   "implement the feature",
			Metadata: map[string]string{"log_path": logPath},
		})
		done <- runResult{result: result, err: err}
	}()

	testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer testCancel()

	// Wait for the prompt message to be submitted, then cancel the run context.
	select {
	case <-api.messageNotify:
	case <-testCtx.Done():
		t.Fatal("timed out waiting for message to be submitted to opencode serve")
	}

	cancelRun()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("adapter.Run returned unexpected error: %v", r.err)
		}
		if r.result.Status != contracts.RunnerResultFailed {
			t.Fatalf("expected RunnerResultFailed after context cancellation, got %s (reason: %q)", r.result.Status, r.result.Reason)
		}
	case <-testCtx.Done():
		t.Fatal("timed out waiting for adapter.Run to return after context cancellation")
	}

	// Graceful teardown must call Stop, not Kill.
	if proc.stopCount == 0 {
		t.Errorf("expected Stop to be called during graceful teardown after cancellation, got stopCount=%d", proc.stopCount)
	}
	if proc.killCount != 0 {
		t.Errorf("expected Kill NOT to be called during graceful teardown, got killCount=%d", proc.killCount)
	}

	// DELETE /session and POST /instance/dispose must be issued on graceful teardown.
	requests := api.Requests()
	foundDelete := false
	foundDispose := false
	for _, req := range requests {
		switch {
		case req.Method == http.MethodDelete && req.Path == "/session/session-1":
			foundDelete = true
		case req.Method == http.MethodPost && req.Path == "/instance/dispose":
			foundDispose = true
		}
	}
	if !foundDelete {
		t.Errorf("expected DELETE /session/session-1 on graceful teardown after cancellation, got %#v", requests)
	}
	if !foundDispose {
		t.Errorf("expected POST /instance/dispose on graceful teardown after cancellation, got %#v", requests)
	}
}
