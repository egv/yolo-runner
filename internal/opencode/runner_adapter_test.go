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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, _ Runner, _ ACPClient) error {
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

func TestCLIRunnerAdapterMapsDeadlineExceededToBlockedTimeout(t *testing.T) {
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
	adapter := &CLIRunnerAdapter{runWithACP: func(context.Context, string, string, string, string, string, string, string, Runner, ACPClient) error {
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
