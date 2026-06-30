package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	acp "github.com/ironpark/acp-go"
)

func writeACPLogFile(t *testing.T, lines ...string) string {
	t.Helper()
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	payload := strings.Join(lines, "\n")
	if payload != "" {
		payload += "\n"
	}
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return logPath
}

func TestCLIRunnerAdapterImplementsContract(t *testing.T) {
	var _ contracts.AgentRunner = (*CLIRunnerAdapter)(nil)
}

func TestCLIRunnerAdapterAcceptsNilContextWhenTimeoutIsSet(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient, _ func(string), _ ...string) error {
		if ctx == nil {
			t.Fatalf("expected non-nil context")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatalf("expected timeout deadline on context")
		}
		return nil
	}}

	result, err := adapter.Run(nil, contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
}

func TestCLIRunnerAdapterNilAdapterReturnsError(t *testing.T) {
	var adapter *CLIRunnerAdapter

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "do x"})
	if err == nil {
		t.Fatalf("expected error for nil adapter, got nil with result %#v", result)
	}
}

func TestCLIRunnerAdapterBuildsCommandFromConfiguredTemplate(t *testing.T) {
	repoRoot := t.TempDir()
	var captured []string
	adapter := &CLIRunnerAdapter{
		command: []string{"opencode", "acp", "--print-logs", "--log-level", "DEBUG", "--cwd", "{{repo_root}}", "--model", "{{model}}", "{{prompt}}", "{{backend}}", "{{task_id}}", "{{mode}}"},
		runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient, _ func(string), command ...string) error {
			captured = append([]string(nil), command...)
			return nil
		},
	}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		RepoRoot: repoRoot,
		Prompt:   "review this",
		Model:    "zai-coding-plan/glm-4.7",
		Mode:     contracts.RunnerModeReview,
		Metadata: map[string]string{"backend": "opencode"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	expected := []string{"opencode", "acp", "--print-logs", "--log-level", "DEBUG", "--cwd", repoRoot, "--model", "zai-coding-plan/glm-4.7", "review this", "opencode", "task-1", string(contracts.RunnerModeReview)}
	if len(captured) != len(expected) {
		t.Fatalf("expected %d command args, got %d: %#v", len(expected), len(captured), captured)
	}
	for i, want := range expected {
		if captured[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, captured[i])
		}
	}
}

func TestCLIRunnerAdapterBuildsDefaultCommandWithConfiguredBinary(t *testing.T) {
	repoRoot := t.TempDir()
	var captured []string
	adapter := &CLIRunnerAdapter{
		binary: "/tmp/custom-opencode",
		runWithACP: func(_ context.Context, _ string, _ string, _ string, _ string, _ string, _ string, _ string, _ Runner, _ ACPClient, _ func(string), command ...string) error {
			captured = append([]string(nil), command...)
			return nil
		},
	}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		RepoRoot: repoRoot,
		Prompt:   "do x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	expected := []string{"/tmp/custom-opencode", "acp", "--print-logs", "--log-level", "DEBUG", "--cwd", repoRoot}
	if len(captured) != len(expected) {
		t.Fatalf("expected %d command args, got %d: %#v", len(expected), len(captured), captured)
	}
	for i, want := range expected {
		if captured[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, captured[i])
		}
	}
}

func TestCLIRunnerAdapterBuildsConfiguredCommandWithConfiguredBinary(t *testing.T) {
	repoRoot := t.TempDir()
	var captured []string
	adapter := NewCLIRunnerAdapter(CommandRunner{}, nil, "", "", "/tmp/custom-opencode", "acp", "--print-logs", "--cwd", "{{repo_root}}")
	adapter.runWithACP = func(_ context.Context, _ string, _ string, _ string, _ string, _ string, _ string, _ string, _ Runner, _ ACPClient, _ func(string), command ...string) error {
		captured = append([]string(nil), command...)
		return nil
	}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		RepoRoot: repoRoot,
		Prompt:   "do x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	expected := []string{"/tmp/custom-opencode", "acp", "--print-logs", "--cwd", repoRoot}
	if len(captured) != len(expected) {
		t.Fatalf("expected %d command args, got %d: %#v", len(expected), len(captured), captured)
	}
	for i, want := range expected {
		if captured[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, captured[i])
		}
	}
}

func TestCLIRunnerAdapterMapsSuccessToCompleted(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient, _ func(string), _ ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient, _ func(string), _ ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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

func TestCLIRunnerAdapterPreservesServeReadinessTimeoutDetailsInResultReason(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-timeout.jsonl")
	healthURL := "http://127.0.0.1:43123/global/health"
	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)

	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, _ string, _ string, _ string, _ string, _ string, _ string, _ string, _ Runner, _ ACPClient, _ func(string), _ ...string) error {
		<-ctx.Done()
		return fmt.Errorf("timed out waiting for opencode serve readiness at %s; stderr log: %s: %w", healthURL, stderrPath, ctx.Err())
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-timeout",
		RepoRoot: t.TempDir(),
		Prompt:   "do x",
		Timeout:  5 * time.Millisecond,
		Metadata: map[string]string{"log_path": logPath},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "runner timeout after 5ms") {
		t.Fatalf("expected readiness timeout to include runner timeout context, got %q", result.Reason)
	}
	if !strings.Contains(result.Reason, healthURL) {
		t.Fatalf("expected readiness timeout to preserve health URL, got %q", result.Reason)
	}
	if !strings.Contains(result.Reason, stderrPath) {
		t.Fatalf("expected readiness timeout to preserve stderr log path, got %q", result.Reason)
	}
	if strings.Contains(result.Reason, context.DeadlineExceeded.Error()) {
		t.Fatalf("expected readiness timeout to omit raw deadline exceeded detail, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterPreservesServeBindFailureDetailsInResultReason(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-bind.jsonl")
	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)
	bindMessage := "listen tcp 127.0.0.1:43123: bind: address already in use"

	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
		return fmt.Errorf("opencode serve exited before readiness; stderr log: %s; stderr: %s", stderrPath, bindMessage)
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-bind",
		RepoRoot: t.TempDir(),
		Prompt:   "do x",
		Metadata: map[string]string{"log_path": logPath},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, bindMessage) {
		t.Fatalf("expected bind failure details, got %q", result.Reason)
	}
	if !strings.Contains(result.Reason, stderrPath) {
		t.Fatalf("expected bind failure to preserve stderr log path, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterMapsInitFailureToFailed(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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

func TestCLIRunnerAdapterExtractsStructuredReviewFailFeedback(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
		lines := []string{
			`{"message":"agent_message \"REVIEW_VERDICT: fail\\n\""}`,
			`{"message":"agent_message \"REVIEW_FAIL_FEEDBACK: missing e2e assertion for retry path\\n\""}`,
		}
		return os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{TaskID: "t-1", RepoRoot: "/repo", Prompt: "review", Mode: contracts.RunnerModeReview, Metadata: map[string]string{"log_path": logPath}})
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

func TestCLIRunnerAdapterLeavesReviewReadyFalseWhenOnlyPassFailTemplatePresent(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "review.jsonl")
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient, func(string), ...string) error {
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

func TestHasStructuredPassVerdictHandlesChunkedVerdictAcrossMessages(t *testing.T) {
	logPath := writeACPLogFile(
		t,
		`{"message":"agent_message \"REVIEW_\""}`,
		`{"message":"agent_message \"VERDICT: pass\""}`,
	)

	if !hasStructuredPassVerdict(logPath) {
		t.Fatalf("expected ReviewReady=true for chunked structured pass verdict")
	}
}

func TestHasStructuredPassVerdictIgnoresInstructionTemplateMentions(t *testing.T) {
	logPath := writeACPLogFile(
		t,
		`{"message":"agent_message \"Use REVIEW_VERDICT: pass only when all checks are green\\n\""}`,
	)

	if hasStructuredPassVerdict(logPath) {
		t.Fatalf("expected ReviewReady=false when REVIEW_VERDICT appears only in instruction text")
	}
}

func TestHasStructuredPassVerdictUsesLastStructuredVerdict(t *testing.T) {
	logPath := writeACPLogFile(
		t,
		`{"message":"agent_message \"REVIEW_VERDICT: pass\\n\""}`,
		`{"message":"agent_message \"REVIEW_VERDICT: fail\\n\""}`,
	)

	if hasStructuredPassVerdict(logPath) {
		t.Fatalf("expected ReviewReady=false when the last structured verdict is fail")
	}
}

func TestHasStructuredPassVerdictAcceptsDoneSuffix(t *testing.T) {
	logPath := writeACPLogFile(
		t,
		`{"message":"agent_message \"REVIEW_VERDICT: passDONE\""}`,
	)

	if !hasStructuredPassVerdict(logPath) {
		t.Fatalf("expected ReviewReady=true for REVIEW_VERDICT with DONE suffix")
	}
}

func TestHasStructuredPassVerdictRejectsFailWithDoneSuffix(t *testing.T) {
	logPath := writeACPLogFile(
		t,
		`{"message":"agent_message \"REVIEW_VERDICT: failDONE\""}`,
	)

	if hasStructuredPassVerdict(logPath) {
		t.Fatalf("expected ReviewReady=false for failing REVIEW_VERDICT with DONE suffix")
	}
}

func TestCLIRunnerAdapterForwardsACPUpdatesToProgressCallback(t *testing.T) {
	seen := []string{}
	seenTypes := []string{}
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpClient ACPClient, onUpdate func(string), command ...string) error {
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
	if seenTypes[0] != "command_run" || seenTypes[1] != "agent_text" || seenTypes[2] != "command_run" {
		t.Fatalf("unexpected forwarded update types: %#v", seenTypes)
	}
}

type acpProgressTestClient struct {
	*acpClient
}

func (c *acpProgressTestClient) Run(context.Context, string, string) error {
	return nil
}

func TestCLIRunnerAdapterRunEmitsStructuredACPCanonicalProgressParity(t *testing.T) {
	client := &acpProgressTestClient{acpClient: &acpClient{taskSessionID: "sess-1"}}
	adapter := &CLIRunnerAdapter{
		acpClient: client,
		runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpRunner ACPClient, onUpdate func(string), command ...string) error {
			c, ok := acpRunner.(*acpProgressTestClient)
			if !ok {
				t.Fatalf("expected *acpProgressTestClient, got %T", acpRunner)
			}
			if err := c.SessionUpdate(ctx, &acp.SessionNotification{
				SessionId: "sess-1",
				Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Exploring repository")),
			}); err != nil {
				t.Fatalf("SessionUpdate(agent message) = %v", err)
			}
			if err := c.SessionUpdate(ctx, &acp.SessionNotification{
				SessionId: "sess-1",
				Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("")),
			}); err != nil {
				t.Fatalf("SessionUpdate(empty message) = %v", err)
			}
			if err := c.SessionUpdate(ctx, &acp.SessionNotification{
				SessionId: "sess-1",
				Update: acp.NewSessionUpdateToolCall(
					acp.ToolCallId("tool-read-1"),
					"Read README.md",
					acp.ToolKindPtr(acp.ToolKindRead),
					acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
					nil, nil,
				),
			}); err != nil {
				t.Fatalf("SessionUpdate(read tool) = %v", err)
			}
			if err := c.SessionUpdate(ctx, &acp.SessionNotification{
				SessionId: "sess-1",
				Update: acp.NewSessionUpdateToolCall(
					acp.ToolCallId("tool-bash-1"),
					"bash: go test ./internal/opencode/",
					acp.ToolKindPtr(acp.ToolKindExecute),
					acp.ToolCallStatusPtr(acp.ToolCallStatusInProgress),
					nil, map[string]any{"command": "go test ./internal/opencode/"},
				),
			}); err != nil {
				t.Fatalf("SessionUpdate(command tool) = %v", err)
			}
			commandUpdate := acp.NewSessionUpdateToolCallUpdate(
				acp.ToolCallId("tool-bash-1"),
				acp.ToolCallStatusPtr(acp.ToolCallStatusCompleted),
				nil, map[string]any{"exit_code": 0},
			)
			commandUpdate.GetToolcallupdate().Kind = acp.ToolKindPtr(acp.ToolKindExecute)
			if err := c.SessionUpdate(ctx, &acp.SessionNotification{
				SessionId: "sess-1",
				Update:    commandUpdate,
			}); err != nil {
				t.Fatalf("SessionUpdate(command update) = %v", err)
			}
			if err := c.SessionUpdate(ctx, &acp.SessionNotification{
				SessionId: "sess-1",
				Update:    acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Editing files")),
			}); err != nil {
				t.Fatalf("SessionUpdate(agent thought) = %v", err)
			}
			if _, err := c.RequestPermission(ctx, &acp.RequestPermissionRequest{
				ToolCall: acp.ToolCallUpdate{
					ToolCallId: acp.ToolCallId("tool-denied-1"),
					Title:      "bash: rm protected.txt",
					Kind:       acp.ToolKindPtr(acp.ToolKindExecute),
					Status:     acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
				},
			}); err != nil {
				t.Fatalf("RequestPermission() = %v", err)
			}
			return nil
		},
	}

	var progress []contracts.RunnerProgress
	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: t.TempDir(),
		Prompt:   "do x",
		OnProgress: func(p contracts.RunnerProgress) {
			progress = append(progress, p)
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}

	gotTypes := make([]string, 0, len(progress))
	for _, p := range progress {
		gotTypes = append(gotTypes, p.Type)
	}
	wantTypes := []string{
		string(contracts.EventTypeAgentText),
		string(contracts.EventTypeToolInvoked),
		string(contracts.EventTypeCommandRun),
		string(contracts.EventTypeCommandRun),
		string(contracts.EventTypeAgentText),
		string(contracts.EventTypeAgentBlocked),
	}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("canonical event types = %#v; want %#v; progress=%#v", gotTypes, wantTypes, progress)
	}
	if progress[1].Metadata["tool_call_id"] != "tool-read-1" || progress[1].Metadata["kind"] != "read" {
		t.Fatalf("tool_invoked identity metadata mismatch: %#v", progress[1])
	}
	if progress[2].Metadata["tool_call_id"] != "tool-bash-1" || progress[2].Metadata["kind"] != "execute" {
		t.Fatalf("command_run identity metadata mismatch: %#v", progress[2])
	}
	blocked := progress[len(progress)-1]
	if blocked.Metadata["reason"] != string(contracts.BlockReasonPermissionDenied) {
		t.Fatalf("blocked reason = %q; want %q; progress=%#v", blocked.Metadata["reason"], contracts.BlockReasonPermissionDenied, blocked)
	}
	if blocked.Metadata["approval_id"] != "tool-denied-1" {
		t.Fatalf("blocked approval_id = %q; want tool-denied-1", blocked.Metadata["approval_id"])
	}
}

func TestCLIRunnerAdapterACPFixtureEmitsCanonicalProgressParity(t *testing.T) {
	var progress []contracts.RunnerProgress
	appendACP := func(notification *acp.SessionNotification) {
		t.Helper()
		p, ok := NormalizeACPProgressNotification(notification)
		if ok {
			progress = append(progress, p)
		}
	}
	appendSession := func(event contracts.TaskSessionEvent) {
		t.Helper()
		p, ok := NormalizeOpencodeTaskSessionEvent(event)
		if ok {
			progress = append(progress, p)
		}
	}

	appendACP(&acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Exploring repository")),
	})
	appendACP(&acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("")),
	})
	appendACP(&acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-read-1"),
			"Read README.md",
			acp.ToolKindPtr(acp.ToolKindRead),
			acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
			nil, nil,
		),
	})
	appendACP(&acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-bash-1"),
			"bash: go test ./internal/opencode/",
			acp.ToolKindPtr(acp.ToolKindExecute),
			acp.ToolCallStatusPtr(acp.ToolCallStatusInProgress),
			nil, map[string]any{"command": "go test ./internal/opencode/"},
		),
	})
	commandUpdate := acp.NewSessionUpdateToolCallUpdate(
		acp.ToolCallId("tool-bash-1"),
		acp.ToolCallStatusPtr(acp.ToolCallStatusCompleted),
		nil, map[string]any{"exit_code": 0},
	)
	commandUpdate.GetToolcallupdate().Kind = acp.ToolKindPtr(acp.ToolKindExecute)
	appendACP(&acp.SessionNotification{
		SessionId: "sess-1",
		Update:    commandUpdate,
	})
	appendACP(&acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Editing files")),
	})

	client := &acpClient{taskSessionID: "sess-1"}
	client.setEventSink(contracts.TaskSessionEventSinkFunc(func(_ context.Context, event contracts.TaskSessionEvent) error {
		appendSession(event)
		return nil
	}))
	if _, err := client.RequestPermission(context.Background(), &acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId("tool-denied-1"),
			Title:      "bash: rm protected.txt",
			Kind:       acp.ToolKindPtr(acp.ToolKindExecute),
			Status:     acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
		},
	}); err != nil {
		t.Fatalf("RequestPermission() = %v", err)
	}
	finished, ok := NormalizeACPPromptResponse(&acp.PromptResponse{StopReason: acp.StopReasonEndTurn})
	if !ok {
		t.Fatalf("expected finish prompt response to normalize")
	}
	progress = append(progress, finished)

	gotTypes := make([]string, 0, len(progress))
	for _, p := range progress {
		gotTypes = append(gotTypes, p.Type)
	}
	wantTypes := []string{
		string(contracts.EventTypeAgentText),
		string(contracts.EventTypeToolInvoked),
		string(contracts.EventTypeCommandRun),
		string(contracts.EventTypeCommandRun),
		string(contracts.EventTypeAgentText),
		string(contracts.EventTypeAgentBlocked),
		string(contracts.EventTypeAgentFinished),
	}
	if strings.Join(gotTypes, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("canonical event types = %#v; want %#v; progress=%#v", gotTypes, wantTypes, progress)
	}
	if progress[1].Metadata["tool_call_id"] != "tool-read-1" || progress[1].Metadata["kind"] != "read" {
		t.Fatalf("tool_invoked identity metadata mismatch: %#v", progress[1])
	}
	if progress[2].Metadata["tool_call_id"] != "tool-bash-1" || progress[2].Metadata["kind"] != "execute" {
		t.Fatalf("command_run identity metadata mismatch: %#v", progress[2])
	}
	blocked := progress[len(progress)-2]
	if blocked.Metadata["reason"] != string(contracts.BlockReasonPermissionDenied) {
		t.Fatalf("blocked reason = %q; want %q; progress=%#v", blocked.Metadata["reason"], contracts.BlockReasonPermissionDenied, blocked)
	}
	if blocked.Metadata["approval_id"] != "tool-denied-1" {
		t.Fatalf("blocked approval_id = %q; want tool-denied-1", blocked.Metadata["approval_id"])
	}
}

func TestNormalizeOpencodeTaskSessionEventApprovedPermissionIsNotPermissionDenied(t *testing.T) {
	progress, ok := NormalizeOpencodeTaskSessionEvent(contracts.TaskSessionEvent{
		Type:      contracts.TaskSessionEventTypeApprovalRequired,
		SessionID: "sess-1",
		Approval: &contracts.TaskSessionApprovalEvent{
			Request: contracts.TaskSessionApprovalRequest{
				ID:    "tool-approved-1",
				Kind:  contracts.TaskSessionApprovalKindToolCall,
				Title: "bash: go test ./internal/opencode/",
			},
			Decision: &contracts.TaskSessionApprovalDecision{
				Outcome: contracts.TaskSessionApprovalApproved,
			},
		},
	})
	if !ok {
		t.Fatalf("expected approved approval event to normalize")
	}
	if progress.Type == string(contracts.EventTypeAgentBlocked) {
		t.Fatalf("approved permission request must not emit agent_blocked: %#v", progress)
	}
	if progress.Metadata["reason"] == string(contracts.BlockReasonPermissionDenied) {
		t.Fatalf("approved permission request must not carry permission_denied: %#v", progress)
	}
}

func TestNormalizeOpencodeTaskSessionEventPermissionRequestWithoutDecisionIsNotDenied(t *testing.T) {
	progress, ok := NormalizeOpencodeTaskSessionEvent(contracts.TaskSessionEvent{
		Type:      contracts.TaskSessionEventTypeApprovalRequired,
		SessionID: "sess-1",
		Approval: &contracts.TaskSessionApprovalEvent{
			Request: contracts.TaskSessionApprovalRequest{
				ID:    "tool-pending-1",
				Kind:  contracts.TaskSessionApprovalKindToolCall,
				Title: "bash: go test ./internal/opencode/",
			},
		},
	})
	if !ok {
		t.Fatalf("expected permission request event to normalize")
	}
	if progress.Type == string(contracts.EventTypeAgentBlocked) {
		t.Fatalf("permission request without denial must not emit agent_blocked: %#v", progress)
	}
	if progress.Metadata["reason"] == string(contracts.BlockReasonPermissionDenied) {
		t.Fatalf("permission request without denial must not carry permission_denied: %#v", progress)
	}
}

func TestNormalizeACPUpdateLineRedactsAndTruncates(t *testing.T) {
	line := "output token sk-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ more text"
	normalized, updateType := normalizeACPUpdateLine(line)
	if updateType != "agent_text" {
		t.Fatalf("expected agent_text, got %q", updateType)
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
	if updateType != "agent_blocked" {
		t.Fatalf("expected agent_blocked for permission request, got %q", updateType)
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
