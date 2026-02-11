package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestCLIRunnerAdapterImplementsContract(t *testing.T) {
	var _ contracts.AgentRunner = (*CLIRunnerAdapter)(nil)
}

func TestCLIRunnerAdapterMapsSuccessToCompleted(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		return nil
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
}

func TestCLIRunnerAdapterMapsStallToBlocked(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		return &StallError{Category: "no_output"}
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status, got %s", result.Status)
	}
}

func TestCLIRunnerAdapterMapsGenericErrorToFailed(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		return errors.New("boom")
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
}

func TestCLIRunnerAdapterAppliesRequestTimeoutToRunContext(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient, _ func(string)) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("expected timeout deadline on context")
		}
		if time.Until(deadline) <= 0 {
			t.Fatalf("expected future deadline")
		}
		return nil
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
}

func TestCLIRunnerAdapterAppliesWatchdogMetadataToRunContext(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient, _ func(string)) error {
		config := watchdogRuntimeConfigFromContext(ctx)
		if config.Timeout != 3*time.Second {
			t.Fatalf("expected watchdog timeout=3s, got %s", config.Timeout)
		}
		if config.Interval != 250*time.Millisecond {
			t.Fatalf("expected watchdog interval=250ms, got %s", config.Interval)
		}
		if config.OpenCodeLogDir != "/tmp/opencode-log" {
			t.Fatalf("expected watchdog log dir to be forwarded, got %q", config.OpenCodeLogDir)
		}
		return nil
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: "/repo",
		Prompt:   "do x",
		Metadata: map[string]string{
			watchdogTimeoutMetadataKey:  "3s",
			watchdogIntervalMetadataKey: "250ms",
			watchdogLogDirMetadataKey:   "/tmp/opencode-log",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
}

func TestCLIRunnerAdapterMapsDeadlineExceededToBlockedTimeout(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		return context.DeadlineExceeded
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x", Timeout: 1 * time.Second})
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

func TestCLIRunnerAdapterMapsInitFailureToFailed(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		return errors.New("serena initialization failed: missing config")
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "serena initialization failed") {
		t.Fatalf("expected init failure reason, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterSetsReviewReadyFromStructuredPassVerdict(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		line := "{\"message\":\"agent_message \\\"REVIEW_VERDICT: pass\\\\n\\\"\"}\n"
		return os.WriteFile(logPath, []byte(line), 0o644)
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "review", Mode: contracts.RunnerModeReview, Metadata: map[string]string{"log_path": logPath}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if !result.ReviewReady {
		t.Fatalf("expected ReviewReady=true when verdict is pass")
	}
}

func TestCLIRunnerAdapterLeavesReviewReadyFalseWhenVerdictMissing(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		line := "{\"message\":\"agent_message \\\"Looks good to me\\\\n\\\"\"}\n"
		return os.WriteFile(logPath, []byte(line), 0o644)
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "review", Mode: contracts.RunnerModeReview, Metadata: map[string]string{"log_path": logPath}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false when verdict is missing")
	}
}

func TestCLIRunnerAdapterLeavesReviewReadyFalseWhenStructuredVerdictFails(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		line := "{\"message\":\"agent_message \\\"REVIEW_VERDICT: fail\\\\n\\\"\"}\n"
		return os.WriteFile(logPath, []byte(line), 0o644)
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "review", Mode: contracts.RunnerModeReview, Metadata: map[string]string{"log_path": logPath}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false when verdict is fail")
	}
}

func TestCLIRunnerAdapterLeavesReviewReadyFalseWhenOnlyPassFailTemplatePresent(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string)) error {
		line := "{\"message\":\"agent_message \\\"Respond with REVIEW_VERDICT: pass/fail and explain why\\\\n\\\"\"}\n"
		return os.WriteFile(logPath, []byte(line), 0o644)
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "review", Mode: contracts.RunnerModeReview, Metadata: map[string]string{"log_path": logPath}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false when only pass/fail template appears")
	}
}

func TestCLIRunnerAdapterForwardsACPUpdatesToProgressCallback(t *testing.T) {
	seen := []string{}
	seenTypes := []string{}
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpClient ACPClient, onUpdate func(string)) error {
		if onUpdate != nil {
			onUpdate("⏳ tool call started: read")
			onUpdate("line output")
			onUpdate("✅ tool call completed: read")
		}
		return nil
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: "/repo",
		Prompt:   "do x",
		OnProgress: func(progress contracts.RunnerProgress) {
			seen = append(seen, progress.Message)
			seenTypes = append(seenTypes, progress.Type)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if len(seen) != 3 {
		t.Fatalf("unexpected forwarded updates: %#v", seen)
	}
	if seenTypes[0] != "runner_cmd_started" || seenTypes[1] != "runner_output" || seenTypes[2] != "runner_cmd_finished" {
		t.Fatalf("unexpected forwarded update types: %#v", seenTypes)
	}
}

func TestNormalizeACPUpdateLineRedactsAndTruncates(t *testing.T) {
	line := "output token sk-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ more text"
	normalized, updateType := normalizeACPUpdateLine(line)
	if updateType != "runner_output" {
		t.Fatalf("expected runner_output, got %q", updateType)
	}
	if strings.Contains(normalized, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("expected token to be redacted, got %q", normalized)
	}
	veryLong := strings.Repeat("x", 1500)
	normalizedLong, _ := normalizeACPUpdateLine(veryLong)
	if len(normalizedLong) > 520 {
		t.Fatalf("expected bounded message length, got %d", len(normalizedLong))
	}
}

func TestNormalizeACPUpdateLineClassifiesPermissionRequestsAsWarnings(t *testing.T) {
	normalized, updateType := normalizeACPUpdateLine("request permission allow")
	if normalized != "request permission allow" {
		t.Fatalf("unexpected normalized line %q", normalized)
	}
	if updateType != "runner_warning" {
		t.Fatalf("expected runner_warning for permission request, got %q", updateType)
	}
}

func TestBuildRunnerArtifactsIncludesStallDiagnostics(t *testing.T) {
	err := &StallError{
		Category:      "question",
		SessionID:     "ses_test",
		LastOutputAge: 42 * time.Second,
		OpenCodeLog:   "/tmp/opencode.log",
		TailPath:      "/tmp/opencode.tail.txt",
	}
	started := time.Date(2026, 2, 11, 13, 0, 0, 0, time.UTC)
	finished := started.Add(90 * time.Second)
	result := contracts.RunnerResult{Status: contracts.RunnerResultBlocked, Reason: err.Error(), StartedAt: started, FinishedAt: finished}
	request := contracts.RunnerRequest{Mode: contracts.RunnerModeImplement, Model: "openai/gpt-5.3-codex"}

	artifacts := buildRunnerArtifacts(request, result, err, "/tmp/run.jsonl")
	if artifacts["status"] != string(contracts.RunnerResultBlocked) {
		t.Fatalf("expected status artifact, got %#v", artifacts)
	}
	if artifacts["backend"] != "opencode" {
		t.Fatalf("expected backend artifact, got %#v", artifacts)
	}
	if artifacts["stall_category"] != "question" {
		t.Fatalf("expected stall category artifact, got %#v", artifacts)
	}
	if artifacts["session_id"] != "ses_test" {
		t.Fatalf("expected session id artifact, got %#v", artifacts)
	}
	if artifacts["last_output_age"] != "42s" {
		t.Fatalf("expected last_output_age artifact, got %#v", artifacts)
	}
}
