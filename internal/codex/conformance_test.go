package codex

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/egv/yolo-runner/internal/contracts"
	"github.com/egv/yolo-runner/internal/contracts/conformance"
)

func TestCLIRunnerAdapterConformance(t *testing.T) {
	conformance.RunAgentRunnerSuite(t, conformance.Config{
		Backend: "codex",
		Model:   "openai/gpt-5.3-codex",
		NewAdapter: func(t *testing.T, scenario conformance.Scenario) contracts.AgentRunner {
			t.Helper()
			return NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
				switch scenario {
				case conformance.ScenarioSuccess:
					_, _ = io.WriteString(spec.Stdout, "working line\n")
					_, _ = io.WriteString(spec.Stderr, "warn line\n")
					return nil
				case conformance.ScenarioReviewPass:
					_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
					return nil
				case conformance.ScenarioReviewFail:
					_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: fail\n")
					return nil
				case conformance.ScenarioTimeoutError:
					_, _ = io.WriteString(spec.Stdout, "still working\n")
					return context.DeadlineExceeded
				case conformance.ScenarioContextTimeoutNoErr:
					_, _ = io.WriteString(spec.Stdout, "still working\n")
					time.Sleep(30 * time.Millisecond)
					return nil
				case conformance.ScenarioFailure:
					_, _ = io.WriteString(spec.Stderr, "boom\n")
					return errors.New(conformance.FailureReason)
				default:
					t.Fatalf("unsupported scenario %q", scenario)
					return nil
				}
			}))
		},
	})
}
