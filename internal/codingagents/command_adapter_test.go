package codingagents

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestResolveCommandArgsRendersBackendPlaceholders(t *testing.T) {
	t.Helper()
	got := CommandSpec{}
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		got = spec
		return nil
	})
	adapter := NewGenericCLIRunnerAdapter("custom-cli", "/usr/bin/custom-cli", []string{
		"--backend={{backend}}",
		"--backend-name={{backend-name}}",
		"--model={{model}}",
		"{{prompt}}",
	}, runner)

	_, _ = adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:      "custom-model",
		Prompt:     "Implement feature",
		TaskID:     "task-1",
		RepoRoot:   t.TempDir(),
		Metadata:   nil,
		Mode:       contracts.RunnerModeImplement,
	})

	if got.Binary != "/usr/bin/custom-cli" {
		t.Fatalf("expected binary %q, got %q", "/usr/bin/custom-cli", got.Binary)
	}
	if !containsSlice(got.Args, "--backend=custom-cli") {
		t.Fatalf("expected --backend placeholder to render, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "--backend-name=custom-cli") {
		t.Fatalf("expected --backend-name placeholder to render, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "--model=custom-model") {
		t.Fatalf("expected --model placeholder to render, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "Implement feature") {
		t.Fatalf("expected prompt placeholder to render, got %#v", got.Args)
	}
}

func TestResolveCommandArgsPreservesNonTemplateValues(t *testing.T) {
	t.Helper()
	got := CommandSpec{}
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		got = spec
		return nil
	})
	adapter := NewGenericCLIRunnerAdapter("", "custom", []string{
		"echo",
		"hello",
	}, runner)

	_, _ = adapter.Run(context.Background(), contracts.RunnerRequest{
		Prompt:  "anything",
		Mode:    contracts.RunnerModeImplement,
		Model:   "model-x",
		TaskID:  "task-2",
		RepoRoot: t.TempDir(),
	})

	if len(got.Args) != 2 {
		t.Fatalf("expected unchanged args count=2, got %d", len(got.Args))
	}
	if got.Args[0] != "echo" || got.Args[1] != "hello" {
		t.Fatalf("expected non-template args preserved, got %#v", got.Args)
	}
}

func TestGeminiBackendAdapterRendersModelInArgs(t *testing.T) {
	t.Helper()
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	definition, ok := catalog.Backend("gemini")
	if !ok {
		t.Fatalf("expected builtin gemini backend")
	}

	got := CommandSpec{}
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		got = spec
		return nil
	})
	adapter := NewGenericCLIRunnerAdapter(definition.Name, definition.Binary, definition.Args, runner)

	_, _ = adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "gemini-2.0-pro",
		Prompt:   "Implement feature",
		TaskID:   "task-1",
		RepoRoot: t.TempDir(),
	})

	if got.Binary != "gemini" {
		t.Fatalf("expected binary %q, got %q", "gemini", got.Binary)
	}
	if !containsSlice(got.Args, "exec") {
		t.Fatalf("expected exec arg to be rendered, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "--model") {
		t.Fatalf("expected --model flag to be rendered, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "gemini-2.0-pro") {
		t.Fatalf("expected selected model in args, got %#v", got.Args)
	}
}

func TestGenericCLIRunnerAdapterUsesLastStructuredReviewVerdictInReviewMode(t *testing.T) {
	t.Helper()
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, err := io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		if err != nil {
			return err
		}
		_, err = io.WriteString(spec.Stdout, "REVIEW_VERDICT: fail\n")
		return err
	})
	adapter := NewGenericCLIRunnerAdapter("codex", "mock", []string{"exec"}, runner)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeReview,
		TaskID:   "task-1",
		RepoRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := strings.TrimSpace(result.Artifacts["review_verdict"]); got != "fail" {
		t.Fatalf("expected review verdict from last marker, got %q", got)
	}
	if result.ReviewReady {
		t.Fatalf("expected review_ready false for fail verdict")
	}
}

func TestGenericCLIRunnerAdapterLeavesReviewFieldsUnsetOutsideReviewMode(t *testing.T) {
	t.Helper()
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, err := io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		return err
	})
	adapter := NewGenericCLIRunnerAdapter("codex", "mock", []string{"exec"}, runner)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeImplement,
		TaskID:   "task-2",
		RepoRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, ok := result.Artifacts["review_verdict"]; ok {
		t.Fatalf("did not expect review_verdict in non-review mode, got %#v", result.Artifacts)
	}
	if result.ReviewReady {
		t.Fatalf("did not expect ReviewReady in non-review mode")
	}
}

func containsSlice(values []string, needle string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}
