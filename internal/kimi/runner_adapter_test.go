package kimi

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestCLIRunnerAdapterImplementsContract(t *testing.T) {
	var _ contracts.AgentRunner = (*CLIRunnerAdapter)(nil)
}

func TestCLIRunnerAdapterRunsKimiAndStreamsProgress(t *testing.T) {
	repoRoot := t.TempDir()
	var gotSpec CommandSpec
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		_, _ = io.WriteString(spec.Stdout, "working line\n")
		_, _ = io.WriteString(spec.Stderr, "warn line\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Model:    "kimi-k2",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if gotSpec.Binary != "kimi-bin" {
		t.Fatalf("expected binary kimi-bin, got %q", gotSpec.Binary)
	}
	expectedArgs := []string{"--print", "--output-format", "text", "--yolo", "--model", "kimi-k2", "--prompt", "implement feature"}
	if !reflect.DeepEqual(gotSpec.Args, expectedArgs) {
		t.Fatalf("unexpected args: %#v", gotSpec.Args)
	}
	if gotSpec.Dir != repoRoot {
		t.Fatalf("expected command dir %q, got %q", repoRoot, gotSpec.Dir)
	}
	expectedLogPath := filepath.Join(repoRoot, "runner-logs", "kimi", "t-1.jsonl")
	if result.LogPath != expectedLogPath {
		t.Fatalf("expected log path %q, got %q", expectedLogPath, result.LogPath)
	}
	if result.Artifacts["backend"] != "kimi" {
		t.Fatalf("expected backend artifact kimi, got %q", result.Artifacts["backend"])
	}
	if len(updates) < 2 {
		t.Fatalf("expected at least 2 progress updates, got %d", len(updates))
	}
	if updates[0].Type != "agent_text" || updates[0].Message != "working line" {
		t.Fatalf("unexpected first update: %#v", updates[0])
	}
	if updates[1].Type != "agent_text" || updates[1].Message != "stderr: warn line" {
		t.Fatalf("unexpected second update: %#v", updates[1])
	}

	stdoutContent, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if !strings.Contains(string(stdoutContent), "working line") {
		t.Fatalf("expected stdout log to contain output, got %q", string(stdoutContent))
	}
	stderrPath := strings.TrimSuffix(result.LogPath, ".jsonl") + ".stderr.log"
	stderrContent, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr log: %v", err)
	}
	if !strings.Contains(string(stderrContent), "warn line") {
		t.Fatalf("expected stderr log to contain output, got %q", string(stderrContent))
	}
}

func TestCLIRunnerAdapterBuildsCommandFromConfiguredArgsTemplate(t *testing.T) {
	repoRoot := t.TempDir()
	var gotSpec CommandSpec
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		return nil
	}), "--backend={{backend}}", "--model", "{{model}}", "--prompt", "{{prompt}}", "--task-id={{task_id}}", "--repo={{repo_root}}", "--mode={{mode}}")

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Model:    "kimi-k2",
		Mode:     contracts.RunnerModeImplement,
		Metadata: map[string]string{"backend": "kimi"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"--backend=kimi", "--model", "kimi-k2", "--prompt", "implement feature", "--task-id=task-1", "--repo=" + repoRoot, "--mode=implement"}
	if !reflect.DeepEqual(gotSpec.Args, expected) {
		t.Fatalf("unexpected templated args: %#v", gotSpec.Args)
	}
}

func TestCLIRunnerAdapterEmitsCanonicalEventsFromKimiJSONL(t *testing.T) {
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"text","text":"  "}],"usage":{"input_tokens":3,"output_tokens":5}}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"text","text":"working through it"},{"type":"tool_use","id":"toolu_1","name":"bash","input":{"command":"go test ./internal/kimi/"}}]}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":false,"content":[{"type":"text","text":"exit_code: 0\nduration_ms: 321\nok"}]}]}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"denial","message":"permission required for network"}`+"\n")
		return nil
	}))

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-canonical",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	textEvents := findProgressByTypeAll(updates, contracts.EventTypeAgentText)
	if len(textEvents) != 1 {
		t.Fatalf("expected one non-empty agent_text event, got %#v", textEvents)
	}
	if textEvents[0].Message != "working through it" {
		t.Fatalf("agent_text message = %q", textEvents[0].Message)
	}

	commandRun := findProgressByType(updates, contracts.EventTypeCommandRun)
	if commandRun == nil {
		t.Fatalf("expected command_run progress, got %#v", updates)
	}
	if commandRun.Metadata["command"] != "go test ./internal/kimi/" {
		t.Fatalf("command = %q", commandRun.Metadata["command"])
	}
	if commandRun.Metadata["exit_code"] != "0" {
		t.Fatalf("exit_code = %q", commandRun.Metadata["exit_code"])
	}
	if commandRun.Metadata["duration_ms"] != "321" {
		t.Fatalf("duration_ms = %q", commandRun.Metadata["duration_ms"])
	}

	tokenUsage := findProgressByType(updates, contracts.EventTypeTokenUsage)
	if tokenUsage == nil {
		t.Fatalf("expected token_usage progress, got %#v", updates)
	}
	if tokenUsage.Metadata["input_tokens"] != "3" {
		t.Fatalf("input_tokens = %q", tokenUsage.Metadata["input_tokens"])
	}
	if tokenUsage.Metadata["output_tokens"] != "5" {
		t.Fatalf("output_tokens = %q", tokenUsage.Metadata["output_tokens"])
	}

	blocked := findProgressByType(updates, contracts.EventTypeAgentBlocked)
	if blocked == nil {
		t.Fatalf("expected agent_blocked progress, got %#v", updates)
	}
	if blocked.Message != "permission required for network" {
		t.Fatalf("agent_blocked message = %q", blocked.Message)
	}
}

func TestCLIRunnerAdapterEmitsCommandRunForFailedKimiCommandToolResult(t *testing.T) {
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fail","name":"bash","input":{"command":"go test ./internal/kimi/"}}]}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_fail","is_error":true,"content":[{"type":"text","text":"exit_code: 1\nduration_ms: 99\nFAIL"}]}]}}`+"\n")
		return nil
	}))

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-canonical-failed-command",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	blocked := findProgressByType(updates, contracts.EventTypeAgentBlocked)
	if blocked == nil {
		t.Fatalf("expected agent_blocked progress, got %#v", updates)
	}
	if blocked.Message != "exit_code: 1 duration_ms: 99 FAIL" {
		t.Fatalf("agent_blocked message = %q", blocked.Message)
	}

	commandRun := findProgressByType(updates, contracts.EventTypeCommandRun)
	if commandRun == nil {
		t.Fatalf("expected command_run progress for failed command, got %#v", updates)
	}
	if commandRun.Metadata["command"] != "go test ./internal/kimi/" {
		t.Fatalf("command = %q", commandRun.Metadata["command"])
	}
	if commandRun.Metadata["outcome"] != "error" {
		t.Fatalf("outcome = %q", commandRun.Metadata["outcome"])
	}
	if commandRun.Metadata["exit_code"] != "1" {
		t.Fatalf("exit_code = %q", commandRun.Metadata["exit_code"])
	}
	if commandRun.Metadata["duration_ms"] != "99" {
		t.Fatalf("duration_ms = %q", commandRun.Metadata["duration_ms"])
	}
}

func TestCLIRunnerAdapterSetsReviewReadyOnStructuredPassVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review",
		RepoRoot: t.TempDir(),
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if !result.ReviewReady {
		t.Fatalf("expected ReviewReady=true for pass verdict")
	}
}

func TestCLIRunnerAdapterLeavesReviewReadyFalseOnStructuredFailVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: failDONE\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review",
		RepoRoot: t.TempDir(),
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false for fail verdict")
	}
}

func TestCLIRunnerAdapterExtractsStructuredReviewFailFeedback(t *testing.T) {
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: fail\n")
		_, _ = io.WriteString(spec.Stdout, "REVIEW_FAIL_FEEDBACK: missing e2e assertion for retry path\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review",
		RepoRoot: t.TempDir(),
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.Artifacts["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict=fail artifact, got %#v", result.Artifacts)
	}
	if result.Artifacts["review_fail_feedback"] != "missing e2e assertion for retry path" {
		t.Fatalf("expected review_fail_feedback artifact, got %#v", result.Artifacts)
	}
}

func TestCLIRunnerAdapterMapsTimeoutToBlocked(t *testing.T) {
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "still working\n")
		return context.DeadlineExceeded
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-timeout",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		Timeout:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "timeout") {
		t.Fatalf("expected timeout reason, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterMapsGenericErrorToFailed(t *testing.T) {
	adapter := NewCLIRunnerAdapter("kimi-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stderr, "boom\n")
		return errors.New("kimi failed")
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-fail",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "kimi failed") {
		t.Fatalf("expected failure reason to contain kimi failed, got %q", result.Reason)
	}
}

func findProgressByType(updates []contracts.RunnerProgress, eventType contracts.EventType) *contracts.RunnerProgress {
	for i := range updates {
		if updates[i].Type == string(eventType) {
			return &updates[i]
		}
	}
	return nil
}

func findProgressByTypeAll(updates []contracts.RunnerProgress, eventType contracts.EventType) []contracts.RunnerProgress {
	matches := []contracts.RunnerProgress{}
	for _, update := range updates {
		if update.Type == string(eventType) {
			matches = append(matches, update)
		}
	}
	return matches
}
