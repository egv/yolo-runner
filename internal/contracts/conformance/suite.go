package conformance

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/internal/contracts"
)

type Scenario string

const (
	ScenarioSuccess             Scenario = "success"
	ScenarioReviewPass          Scenario = "review_pass"
	ScenarioReviewFail          Scenario = "review_fail"
	ScenarioTimeoutError        Scenario = "timeout_error"
	ScenarioContextTimeoutNoErr Scenario = "context_timeout_no_error"
	ScenarioFailure             Scenario = "failure"
	FailureReason                        = "forced backend failure"
	defaultTimeout                       = 5 * time.Millisecond
)

type AdapterFactory func(t *testing.T, scenario Scenario) contracts.AgentRunner

type Config struct {
	Backend    string
	Model      string
	NewAdapter AdapterFactory
}

func RunAgentRunnerSuite(t *testing.T, cfg Config) {
	t.Helper()

	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		t.Fatal("conformance backend is required")
	}
	if cfg.NewAdapter == nil {
		t.Fatal("conformance adapter factory is required")
	}

	t.Run("success maps to completed and emits progress", func(t *testing.T) {
		result, updates := runScenario(t, cfg, ScenarioSuccess, contracts.RunnerModeImplement, 0)
		assertStatus(t, result, contracts.RunnerResultCompleted)
		if len(updates) == 0 {
			t.Fatalf("expected progress updates for success scenario")
		}
		if strings.TrimSpace(updates[0].Type) == "" {
			t.Fatalf("expected non-empty progress type")
		}
		if strings.TrimSpace(updates[0].Message) == "" {
			t.Fatalf("expected non-empty progress message")
		}
	})

	t.Run("review pass sets review ready", func(t *testing.T) {
		result, _ := runScenario(t, cfg, ScenarioReviewPass, contracts.RunnerModeReview, 0)
		assertStatus(t, result, contracts.RunnerResultCompleted)
		if !result.ReviewReady {
			t.Fatalf("expected ReviewReady=true when structured verdict is pass")
		}
	})

	t.Run("review fail keeps review ready false", func(t *testing.T) {
		result, _ := runScenario(t, cfg, ScenarioReviewFail, contracts.RunnerModeReview, 0)
		assertStatus(t, result, contracts.RunnerResultCompleted)
		if result.ReviewReady {
			t.Fatalf("expected ReviewReady=false when structured verdict is fail")
		}
	})

	t.Run("timeout error maps to blocked", func(t *testing.T) {
		result, _ := runScenario(t, cfg, ScenarioTimeoutError, contracts.RunnerModeImplement, defaultTimeout)
		assertStatus(t, result, contracts.RunnerResultBlocked)
		if !strings.Contains(strings.ToLower(result.Reason), "timeout") {
			t.Fatalf("expected timeout reason, got %q", result.Reason)
		}
	})

	t.Run("context timeout without runner error maps to blocked", func(t *testing.T) {
		result, _ := runScenario(t, cfg, ScenarioContextTimeoutNoErr, contracts.RunnerModeImplement, defaultTimeout)
		assertStatus(t, result, contracts.RunnerResultBlocked)
		if !strings.Contains(strings.ToLower(result.Reason), "timeout") {
			t.Fatalf("expected timeout reason, got %q", result.Reason)
		}
	})

	t.Run("generic error maps to failed", func(t *testing.T) {
		result, _ := runScenario(t, cfg, ScenarioFailure, contracts.RunnerModeImplement, 0)
		assertStatus(t, result, contracts.RunnerResultFailed)
		if !strings.Contains(result.Reason, FailureReason) {
			t.Fatalf("expected reason to contain %q, got %q", FailureReason, result.Reason)
		}
	})
}

func runScenario(t *testing.T, cfg Config, scenario Scenario, mode contracts.RunnerMode, timeout time.Duration) (contracts.RunnerResult, []contracts.RunnerProgress) {
	t.Helper()

	repoRoot := t.TempDir()
	updates := []contracts.RunnerProgress{}
	request := contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Mode:     mode,
		Model:    cfg.Model,
		Timeout:  timeout,
		Metadata: map[string]string{
			"log_path": filepath.Join(repoRoot, "runner-logs", cfg.Backend, "t-1.jsonl"),
		},
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	}

	if mode == contracts.RunnerModeReview {
		request.Prompt = "review"
	}

	adapter := cfg.NewAdapter(t, scenario)
	result, err := adapter.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("adapter returned unexpected error: %v", err)
	}
	return result, updates
}

func assertStatus(t *testing.T, result contracts.RunnerResult, want contracts.RunnerResultStatus) {
	t.Helper()
	if result.Status != want {
		t.Fatalf("expected status=%s, got %s", want, result.Status)
	}
	if result.Artifacts["backend"] == "" {
		t.Fatalf("expected backend artifact, got %#v", result.Artifacts)
	}
	if result.Artifacts["status"] != string(result.Status) {
		t.Fatalf("expected status artifact=%s, got %q", result.Status, result.Artifacts["status"])
	}
}
