package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/contracts/conformance"
)

func TestCLIRunnerAdapterConformance(t *testing.T) {
	conformance.RunAgentRunnerSuite(t, conformance.Config{
		Backend: "opencode",
		Model:   "gpt-5",
		NewAdapter: func(t *testing.T, scenario conformance.Scenario) contracts.AgentRunner {
			t.Helper()
			return &CLIRunnerAdapter{
				runWithACP: func(_ context.Context, _ string, _ string, _ string, _ string, _ string, _ string, logPath string, _ Runner, _ ACPClient, onUpdate func(string), _ ...string) error {
					switch scenario {
					case conformance.ScenarioSuccess:
						if onUpdate != nil {
							onUpdate("working line")
							onUpdate("warn line")
						}
						return nil
					case conformance.ScenarioReviewPass:
						return writeStructuredReviewVerdict(logPath, "pass")
					case conformance.ScenarioReviewFail:
						return writeStructuredReviewVerdict(logPath, "fail")
					case conformance.ScenarioTimeoutError:
						if onUpdate != nil {
							onUpdate("still working")
						}
						return context.DeadlineExceeded
					case conformance.ScenarioContextTimeoutNoErr:
						if onUpdate != nil {
							onUpdate("still working")
						}
						time.Sleep(30 * time.Millisecond)
						return nil
					case conformance.ScenarioFailure:
						return errors.New(conformance.FailureReason)
					default:
						t.Fatalf("unsupported scenario %q", scenario)
						return nil
					}
				},
			}
		},
	})
}

func writeStructuredReviewVerdict(logPath string, verdict string) error {
	if logPath == "" {
		return errors.New("log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	line := `{"message":"agent_message \"REVIEW_VERDICT: ` + verdict + `\\n\""}` + "\n"
	return os.WriteFile(logPath, []byte(line), 0o644)
}
