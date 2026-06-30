package claude

import (
	"context"
	"errors"
	"fmt"
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

func TestCLIRunnerAdapterRunsClaudeAndStreamsProgress(t *testing.T) {
	repoRoot := t.TempDir()
	var gotSpec CommandSpec
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		_, _ = io.WriteString(spec.Stdout, "working line\n")
		_, _ = io.WriteString(spec.Stderr, "warn line\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Model:    "claude-3-5-sonnet",
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
	if gotSpec.Binary != "claude-bin" {
		t.Fatalf("expected binary claude-bin, got %q", gotSpec.Binary)
	}
	expectedArgs := []string{"--print", "--output-format", "text", "--model", "claude-3-5-sonnet", "--prompt", "implement feature"}
	if !reflect.DeepEqual(gotSpec.Args, expectedArgs) {
		t.Fatalf("unexpected args: %#v", gotSpec.Args)
	}
	if gotSpec.Dir != repoRoot {
		t.Fatalf("expected command dir %q, got %q", repoRoot, gotSpec.Dir)
	}
	expectedLogPath := filepath.Join(repoRoot, "runner-logs", "claude", "t-1.jsonl")
	if result.LogPath != expectedLogPath {
		t.Fatalf("expected log path %q, got %q", expectedLogPath, result.LogPath)
	}
	if result.Artifacts["backend"] != "claude" {
		t.Fatalf("expected backend artifact claude, got %q", result.Artifacts["backend"])
	}
	if len(updates) < 2 {
		t.Fatalf("expected at least 2 progress updates, got %d", len(updates))
	}
	if updates[0].Type != "runner_output" || updates[0].Message != "working line" {
		t.Fatalf("unexpected first update: %#v", updates[0])
	}
	if updates[1].Type != "runner_output" || updates[1].Message != "stderr: warn line" {
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
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		return nil
	}), "--backend={{backend}}", "--model", "{{model}}", "--prompt", "{{prompt}}", "--task-id={{task_id}}", "--repo={{repo_root}}", "--mode={{mode}}")

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Model:    "claude-opus-4.1",
		Mode:     contracts.RunnerModeImplement,
		Metadata: map[string]string{"backend": "claude"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"--backend=claude", "--model", "claude-opus-4.1", "--prompt", "implement feature", "--task-id=task-1", "--repo=" + repoRoot, "--mode=implement"}
	if !reflect.DeepEqual(gotSpec.Args, expected) {
		t.Fatalf("unexpected templated args: %#v", gotSpec.Args)
	}
}

func TestCLIRunnerAdapterEmitsCommandRunFromClaudeBashToolResult(t *testing.T) {
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./internal/claude/"}}]}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":false,"content":[{"type":"text","text":"exit_code: 0\nduration_ms: 1234\nok"}]}]}}`+"\n")
		return nil
	}))

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-command",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	commandRun := findProgressByType(updates, contracts.EventTypeCommandRun)
	if commandRun == nil {
		t.Fatalf("expected command_run progress, got %#v", updates)
	}
	if commandRun.Metadata["command"] != "go test ./internal/claude/" {
		t.Fatalf("command = %q", commandRun.Metadata["command"])
	}
	if commandRun.Metadata["exit_code"] != "0" {
		t.Fatalf("exit_code = %q", commandRun.Metadata["exit_code"])
	}
	if commandRun.Metadata["duration_ms"] != "1234" {
		t.Fatalf("duration_ms = %q", commandRun.Metadata["duration_ms"])
	}
}

func TestCLIRunnerAdapterEmitsToolInvokedFromClaudeEditToolResult(t *testing.T) {
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Edit","input":{"file_path":"internal/claude/runner_adapter.go"}}]}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","is_error":false,"content":[{"type":"text","text":"updated"}]}]}}`+"\n")
		return nil
	}))

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-tool",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolInvoked := findProgressByType(updates, contracts.EventTypeToolInvoked)
	if toolInvoked == nil {
		t.Fatalf("expected tool_invoked progress, got %#v", updates)
	}
	if toolInvoked.Metadata["tool"] != "Edit" {
		t.Fatalf("tool = %q", toolInvoked.Metadata["tool"])
	}
	if toolInvoked.Metadata["target"] != "internal/claude/runner_adapter.go" {
		t.Fatalf("target = %q", toolInvoked.Metadata["target"])
	}
	if toolInvoked.Metadata["outcome"] != "ok" {
		t.Fatalf("outcome = %q", toolInvoked.Metadata["outcome"])
	}
}

func TestCLIRunnerAdapterEmitsProgressFromTopLevelClaudeToolResults(t *testing.T) {
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, `{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"go test ./internal/claude/"}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"tool_result","tool_use_id":"toolu_bash","is_error":false,"content":[{"type":"text","text":"exit_code: 0\nduration_ms: 250\nok"}]}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"tool_use","id":"toolu_edit","name":"Edit","input":{"file_path":"internal/claude/runner_adapter.go"}}`+"\n")
		_, _ = io.WriteString(spec.Stdout, `{"type":"tool_result","tool_use_id":"toolu_edit","is_error":false,"content":[{"type":"text","text":"updated"}]}`+"\n")
		return nil
	}))

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-top-level-tools",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	commandRun := findProgressByType(updates, contracts.EventTypeCommandRun)
	if commandRun == nil {
		t.Fatalf("expected command_run progress, got %#v", updates)
	}
	if commandRun.Metadata["command"] != "go test ./internal/claude/" {
		t.Fatalf("command = %q", commandRun.Metadata["command"])
	}
	if commandRun.Metadata["exit_code"] != "0" {
		t.Fatalf("exit_code = %q", commandRun.Metadata["exit_code"])
	}

	toolInvoked := findProgressByType(updates, contracts.EventTypeToolInvoked)
	if toolInvoked == nil {
		t.Fatalf("expected tool_invoked progress, got %#v", updates)
	}
	if toolInvoked.Metadata["tool"] != "Edit" {
		t.Fatalf("tool = %q", toolInvoked.Metadata["tool"])
	}
	if toolInvoked.Metadata["target"] != "internal/claude/runner_adapter.go" {
		t.Fatalf("target = %q", toolInvoked.Metadata["target"])
	}
	if toolInvoked.Metadata["outcome"] != "ok" {
		t.Fatalf("outcome = %q", toolInvoked.Metadata["outcome"])
	}
}

func TestCLIRunnerAdapterEmitsTokenUsageFromClaudeAssistantUsage(t *testing.T) {
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, `{"type":"assistant","message":{"usage":{"input_tokens":42,"output_tokens":7},"content":[{"type":"text","text":"done"}]}}`+"\n")
		return nil
	}))

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-usage",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokenUsage := findProgressByType(updates, contracts.EventTypeTokenUsage)
	if tokenUsage == nil {
		t.Fatalf("expected token_usage progress, got %#v", updates)
	}
	if tokenUsage.Metadata["input_tokens"] != "42" {
		t.Fatalf("input_tokens = %q", tokenUsage.Metadata["input_tokens"])
	}
	if tokenUsage.Metadata["output_tokens"] != "7" {
		t.Fatalf("output_tokens = %q", tokenUsage.Metadata["output_tokens"])
	}
}

func TestSessionRunnerAdapterEmitsCommandRunFromClaudeBashToolResult(t *testing.T) {
	binary := writeClaudeFixture(t,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"go test ./internal/claude/"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":false,"content":[{"type":"text","text":"exit_code: 0\nduration_ms: 1234\nok"}]}]}}`,
		`{"type":"result","subtype":"success"}`,
	)
	updates := runSessionAdapterFixture(t, binary)

	commandRun := findProgressByType(updates, contracts.EventTypeCommandRun)
	if commandRun == nil {
		t.Fatalf("expected command_run progress, got %#v", updates)
	}
	if commandRun.Metadata["command"] != "go test ./internal/claude/" {
		t.Fatalf("command = %q", commandRun.Metadata["command"])
	}
	if commandRun.Metadata["exit_code"] != "0" {
		t.Fatalf("exit_code = %q", commandRun.Metadata["exit_code"])
	}
	if commandRun.Metadata["duration_ms"] != "1234" {
		t.Fatalf("duration_ms = %q", commandRun.Metadata["duration_ms"])
	}
}

func TestSessionRunnerAdapterEmitsToolInvokedFromClaudeEditToolResult(t *testing.T) {
	binary := writeClaudeFixture(t,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_2","name":"Edit","input":{"file_path":"internal/claude/runner_adapter.go"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","is_error":false,"content":[{"type":"text","text":"updated"}]}]}}`,
		`{"type":"result","subtype":"success"}`,
	)
	updates := runSessionAdapterFixture(t, binary)

	toolInvoked := findProgressByType(updates, contracts.EventTypeToolInvoked)
	if toolInvoked == nil {
		t.Fatalf("expected tool_invoked progress, got %#v", updates)
	}
	if toolInvoked.Metadata["tool"] != "Edit" {
		t.Fatalf("tool = %q", toolInvoked.Metadata["tool"])
	}
	if toolInvoked.Metadata["target"] != "internal/claude/runner_adapter.go" {
		t.Fatalf("target = %q", toolInvoked.Metadata["target"])
	}
	if toolInvoked.Metadata["outcome"] != "ok" {
		t.Fatalf("outcome = %q", toolInvoked.Metadata["outcome"])
	}
}

func TestSessionRunnerAdapterEmitsTokenUsageFromClaudeAssistantUsage(t *testing.T) {
	binary := writeClaudeFixture(t,
		`{"type":"assistant","message":{"usage":{"input_tokens":42,"output_tokens":7},"content":[{"type":"text","text":"done"}]}}`,
		`{"type":"result","subtype":"success"}`,
	)
	updates := runSessionAdapterFixture(t, binary)

	tokenUsage := findProgressByType(updates, contracts.EventTypeTokenUsage)
	if tokenUsage == nil {
		t.Fatalf("expected token_usage progress, got %#v", updates)
	}
	if tokenUsage.Metadata["input_tokens"] != "42" {
		t.Fatalf("input_tokens = %q", tokenUsage.Metadata["input_tokens"])
	}
	if tokenUsage.Metadata["output_tokens"] != "7" {
		t.Fatalf("output_tokens = %q", tokenUsage.Metadata["output_tokens"])
	}
}

func TestCLIRunnerAdapterSetsReviewReadyOnStructuredPassVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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

func findProgressByType(updates []contracts.RunnerProgress, eventType contracts.EventType) *contracts.RunnerProgress {
	for i := range updates {
		if updates[i].Type == string(eventType) {
			return &updates[i]
		}
	}
	return nil
}

func writeClaudeFixture(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-fixture.sh")
	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	for _, line := range lines {
		script.WriteString("printf '%s\\n' '")
		script.WriteString(strings.ReplaceAll(line, "'", "'\\''"))
		script.WriteString("'\n")
	}
	script.WriteString("sleep 0.2\n")
	if err := os.WriteFile(path, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func runSessionAdapterFixture(t *testing.T, binary string) []contracts.RunnerProgress {
	t.Helper()
	updates := []contracts.RunnerProgress{}
	adapter := NewSessionRunnerAdapter(binary)
	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-session",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("status = %s, reason = %q, updates = %#v", result.Status, result.Reason, updates)
	}
	return updates
}

func TestCLIRunnerAdapterLeavesReviewReadyFalseOnStructuredFailVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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

func TestStdinTaskSessionExecuteEmitsPermissionDeniedAgentBlocked(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)

	detail := "Claude requested permissions for Bash, but you haven't granted them."
	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"system","subtype":"init"}`)
		_, _ = fmt.Fprintf(stdoutW, `{"type":"assistant","message":{"content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":%q}]}]}}`+"\n", detail)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	var progress []contracts.RunnerProgress
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, e contracts.TaskSessionEvent) error {
		if p, ok := contracts.NormalizeTaskSessionEvent(e); ok {
			progress = append(progress, p)
		}
		return nil
	})
	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p", EventSink: sink}); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}

	var blocked []contracts.RunnerProgress
	for _, p := range progress {
		if p.Type == string(contracts.EventTypeAgentBlocked) {
			blocked = append(blocked, p)
		}
	}
	if len(blocked) != 1 {
		t.Fatalf("got %d agent_blocked progress events; want 1; progress=%#v", len(blocked), progress)
	}
	if blocked[0].Metadata["reason"] != string(contracts.BlockReasonPermissionDenied) {
		t.Fatalf("reason = %q; want %q", blocked[0].Metadata["reason"], contracts.BlockReasonPermissionDenied)
	}
	if blocked[0].Metadata["detail"] != detail {
		t.Fatalf("detail = %q; want %q", blocked[0].Metadata["detail"], detail)
	}
}

func TestStdinTaskSessionExecuteDropsEmptyAssistantText(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)

	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"system","subtype":"init"}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"bash"}]}}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"assistant","message":{"content":[{"type":"text","text":""}]}}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	var progress []contracts.RunnerProgress
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, e contracts.TaskSessionEvent) error {
		if p, ok := contracts.NormalizeTaskSessionEvent(e); ok {
			progress = append(progress, p)
		}
		return nil
	})
	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p", EventSink: sink}); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}
	for _, p := range progress {
		if p.Type == string(contracts.EventTypeAgentText) || (p.Type == string(contracts.EventTypeRunnerOutput) && strings.TrimSpace(p.Message) == "") {
			t.Fatalf("empty agent text/output should be dropped, got progress=%#v", progress)
		}
	}
}

func TestCLIRunnerAdapterMapsTimeoutToBlocked(t *testing.T) {
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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
	adapter := NewCLIRunnerAdapter("claude-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stderr, "boom\n")
		return errors.New("claude failed")
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
	if !strings.Contains(result.Reason, "claude failed") {
		t.Fatalf("expected failure reason to contain claude failed, got %q", result.Reason)
	}
}
