package opencode

import (
	"context"
	"errors"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// ServeRunnerAdapter implements contracts.AgentRunner backed by the opencode serve
// HTTP API. Each Run() call launches a dedicated opencode serve process, waits for
// it to be healthy, submits the prompt, waits for a terminal SSE event, then
// tears down the process.
type ServeRunnerAdapter struct {
	runtime contracts.TaskSessionRuntime
	binary  string
}

// NewServeRunnerAdapter returns an AgentRunner that uses `opencode serve` mode.
func NewServeRunnerAdapter(binary string, args ...string) *ServeRunnerAdapter {
	return &ServeRunnerAdapter{
		runtime: NewTaskSessionRuntime(binary, args...),
		binary:  binary,
	}
}

// serveSessionWaiter is implemented by ServeTaskSession and allows
// waitForServeSessionCompletion to block until the underlying serve process exits.
type serveSessionWaiter interface {
	waitWithContext(ctx context.Context) error
}

var _ contracts.AgentRunner = (*ServeRunnerAdapter)(nil)

func (a *ServeRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := time.Now().UTC()

	runCtx, cancel := contracts.WithOptionalTimeout(ctx, request.Timeout)
	defer cancel()

	metadata := make(map[string]string, len(request.Metadata))
	for k, v := range request.Metadata {
		metadata[k] = v
	}

	startReq := contracts.TaskSessionStartRequest{
		TaskID:   request.TaskID,
		Backend:  "opencode-serve",
		RepoRoot: request.RepoRoot,
		LogPath:  request.Metadata["log_path"],
		Metadata: metadata,
	}

	session, err := a.runtime.Start(runCtx, startReq)
	if err != nil {
		return contracts.NormalizeBackendRunnerResult(startedAt, time.Now().UTC(), request, err, nil), nil
	}

	if err := session.WaitReady(runCtx); err != nil {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true})
		return contracts.NormalizeBackendRunnerResult(startedAt, time.Now().UTC(), request, err, nil), nil
	}

	var sink contracts.TaskSessionEventSink
	if request.OnProgress != nil {
		onProgress := request.OnProgress
		sink = contracts.TaskSessionEventSinkFunc(func(_ context.Context, e contracts.TaskSessionEvent) error {
			if progress, ok := contracts.NormalizeTaskSessionEvent(e); ok {
				onProgress(progress)
			}
			return nil
		})
	}

	execReq := contracts.TaskSessionExecuteRequest{
		Prompt:    request.Prompt,
		Model:     request.Model,
		Mode:      request.Mode,
		Metadata:  request.Metadata,
		EventSink: sink,
	}

	runErr := session.Execute(runCtx, execReq)
	if runErr == nil {
		runErr = waitForServeSessionCompletion(runCtx, session)
	}
	runErr = contracts.FinalizeRunError(runCtx, runErr)

	_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: runErr != nil && !errors.Is(runErr, context.Canceled)})

	return contracts.NormalizeBackendRunnerResult(startedAt, time.Now().UTC(), request, runErr, nil), nil
}

// waitForServeSessionCompletion waits for the serve session to finish.
// When the session implements serveSessionWaiter it blocks until the underlying
// serve process exits or the context is cancelled; otherwise it falls back to
// blocking until the context is done.
func waitForServeSessionCompletion(ctx context.Context, session contracts.TaskSession) error {
	if ctx == nil {
		return nil
	}
	if w, ok := session.(serveSessionWaiter); ok {
		return w.waitWithContext(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}
