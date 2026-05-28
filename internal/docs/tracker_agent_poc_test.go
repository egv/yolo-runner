package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrackerAgentPOCRunbookDocumentsOperatorWorkflow(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	runbookPath := filepath.Join(repoRoot, "docs", "tracker-agent-poc.md")
	contents, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read tracker agent PoC runbook: %v", err)
	}

	runbook := string(contents)
	required := []string{
		"yolo-tracker-agent-poc-vay",
		"./bin/yolo-agent --repo . --root yolo-tracker-agent-poc-vay --profile beads",
		"./bin/yolo-agent tracker-watch --repo . --profile startrek-poc --once --dry-run",
		"landing:\n  type: arc-pr",
		"tracker_agent:",
		"STARTREK_TOKEN",
		"GITHUB_TOKEN",
		"yolo-agent-ready",
		"yolo-agent-in-progress",
		"needs-info",
		"br sync --flush-only",
		".yolo-runner/tracker-agent.lock",
		"Known Limitations",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("tracker agent PoC runbook missing %q", needle)
		}
	}
}

func TestReadmeLinksTrackerAgentPOCRunbook(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	readme := string(contents)
	if !strings.Contains(readme, "docs/tracker-agent-poc.md") {
		t.Fatalf("README missing tracker agent PoC runbook link")
	}
}
