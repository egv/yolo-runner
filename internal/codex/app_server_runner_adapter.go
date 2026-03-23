package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// AppServerRunnerAdapter implements contracts.AgentRunner backed by the codex
// app-server protocol. Each Run() call launches a dedicated codex app-server
// process, runs a single thread/turn, then tears down the process.
type AppServerRunnerAdapter struct {
	runtime contracts.TaskSessionRuntime
	now     func() time.Time
}

var _ contracts.AgentRunner = (*AppServerRunnerAdapter)(nil)

// NewAppServerRunnerAdapter returns an AgentRunner that uses `codex app-server` mode.
func NewAppServerRunnerAdapter(binary string, args ...string) *AppServerRunnerAdapter {
	return &AppServerRunnerAdapter{
		runtime: NewTaskSessionRuntime(binary, args...),
		now:     time.Now,
	}
}

func (a *AppServerRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.now == nil {
		a.now = time.Now
	}

	startedAt := a.now().UTC()
	logPath := resolveLogPath(request)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return contracts.RunnerResult{}, err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer logFile.Close()

	runCtx, cancel := contracts.WithOptionalTimeout(ctx, request.Timeout)
	defer cancel()

	session, err := a.runtime.Start(runCtx, contracts.TaskSessionStartRequest{
		TaskID:   request.TaskID,
		Backend:  "codex",
		RepoRoot: request.RepoRoot,
		Metadata: request.Metadata,
	})
	if err != nil {
		runErr := contracts.FinalizeRunError(runCtx, err)
		finishedAt := a.now().UTC()
		result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)
		result.LogPath = logPath
		result.Artifacts = buildRunnerArtifacts(request, result)
		return result, nil
	}

	if err := session.WaitReady(runCtx); err != nil {
		runErr := contracts.FinalizeRunError(runCtx, err)
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true})
		finishedAt := a.now().UTC()
		result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)
		result.LogPath = logPath
		result.Artifacts = buildRunnerArtifacts(request, result)
		return result, nil
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

	runErr := session.Execute(runCtx, contracts.TaskSessionExecuteRequest{
		Prompt:    request.Prompt,
		Model:     request.Model,
		Mode:      request.Mode,
		Metadata:  request.Metadata,
		EventSink: sink,
	})
	runErr = contracts.FinalizeRunError(runCtx, runErr)

	teardownForce := runErr != nil && !errors.Is(runErr, context.Canceled)
	_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: teardownForce})

	finishedAt := a.now().UTC()
	result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)
	result.LogPath = logPath
	result.Artifacts = buildRunnerArtifacts(request, result)
	return result, nil
}
