package claude

import (
	"context"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// SessionRunnerAdapter implements contracts.AgentRunner backed by the claude
// stdin/stdout TaskSessionRuntime. Each Run() call spawns one claude process,
// passes the prompt as a CLI argument, reads stream-json events, then tears down.
type SessionRunnerAdapter struct {
	runtime *TaskSessionRuntime
}

// NewSessionRunnerAdapter returns an AgentRunner that uses the claude CLI in
// --output-format stream-json (--print) mode.
func NewSessionRunnerAdapter(binary string) *SessionRunnerAdapter {
	return &SessionRunnerAdapter{runtime: NewTaskSessionRuntime(binary)}
}

var _ contracts.AgentRunner = (*SessionRunnerAdapter)(nil)

// buildClaudeArgs returns the full claude CLI argument list with the prompt as
// the last positional argument. Passing the prompt via args (not stdin) means
// claude processes it immediately without waiting for stdin EOF.
func buildClaudeArgs(model, prompt string) []string {
	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if m := strings.TrimSpace(model); m != "" {
		args = append(args, "--model", m)
	}
	return append(args, prompt)
}

func (a *SessionRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
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
		RepoRoot: request.RepoRoot,
		Metadata: metadata,
		// Pass the prompt as a CLI argument so claude processes it immediately
		// without waiting for stdin input.
		Command: buildClaudeArgs(request.Model, request.Prompt),
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
	runErr = contracts.FinalizeRunError(runCtx, runErr)

	_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: runErr != nil})

	finishedAt := time.Now().UTC()
	result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)

	if ts, ok := session.(*StdinTaskSession); ok {
		logPath := ts.LogPath()
		result.LogPath = logPath
		if result.Status == contracts.RunnerResultCompleted && request.Mode == contracts.RunnerModeReview {
			result.ReviewReady = hasStructuredPassVerdict(logPath)
		}
		result.Artifacts = contracts.BuildRunnerArtifacts("claude", request, result, buildSessionExtras(request, result, logPath))
	}

	return result, nil
}

func buildSessionExtras(request contracts.RunnerRequest, result contracts.RunnerResult, logPath string) map[string]string {
	if request.Mode != contracts.RunnerModeReview {
		return nil
	}
	extras := map[string]string{}
	if verdict, ok := structuredReviewVerdict(logPath); ok {
		extras["review_verdict"] = verdict
		if verdict == "fail" {
			if feedback, ok := structuredReviewFailFeedback(logPath); ok {
				extras["review_fail_feedback"] = feedback
			}
		}
	}
	return extras
}
