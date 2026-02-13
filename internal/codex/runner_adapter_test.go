package codex

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

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestCLIRunnerAdapterImplementsContract(t *testing.T) {
	var _ contracts.AgentRunner = (*CLIRunnerAdapter)(nil)
}

func TestCLIRunnerAdapterRunsCodexAndStreamsProgress(t *testing.T) {
	repoRoot := t.TempDir()
	var gotSpec CommandSpec
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		_, _ = io.WriteString(spec.Stdout, "working line\n")
		_, _ = io.WriteString(spec.Stderr, "warn line\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Model:    "openai/gpt-5.3-codex",
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
	if gotSpec.Binary != "codex-bin" {
		t.Fatalf("expected binary codex-bin, got %q", gotSpec.Binary)
	}
	expectedArgs := []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--model", "openai/gpt-5.3-codex", "implement feature"}
	if !reflect.DeepEqual(gotSpec.Args, expectedArgs) {
		t.Fatalf("unexpected args: %#v", gotSpec.Args)
	}
	if gotSpec.Dir != repoRoot {
		t.Fatalf("expected command dir %q, got %q", repoRoot, gotSpec.Dir)
	}
	expectedLogPath := filepath.Join(repoRoot, "runner-logs", "codex", "t-1.jsonl")
	if result.LogPath != expectedLogPath {
		t.Fatalf("expected log path %q, got %q", expectedLogPath, result.LogPath)
	}
	if result.Artifacts["backend"] != "codex" {
		t.Fatalf("expected backend artifact codex, got %q", result.Artifacts["backend"])
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

func TestCLIRunnerAdapterSetsReviewReadyOnStructuredPassVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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

func TestCLIRunnerAdapterMapsTimeoutToBlocked(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
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

func TestCLIRunnerAdapterMapsContextTimeoutToBlockedEvenWhenRunnerReturnsNil(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "still working\n")
		time.Sleep(30 * time.Millisecond)
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-timeout",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		Timeout:  5 * time.Millisecond,
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
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stderr, "boom\n")
		return errors.New("codex failed")
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
	if !strings.Contains(result.Reason, "codex failed") {
		t.Fatalf("expected failure reason to contain codex failed, got %q", result.Reason)
	}
}
