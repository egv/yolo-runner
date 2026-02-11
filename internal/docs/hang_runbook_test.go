package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHangRunbookDocumentsCoreLogPathsAndFlags(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	runbookPath := filepath.Join(repoRoot, "docs", "hang-triage.md")
	contents, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read runbook: %v", err)
	}

	runbook := string(contents)
	required := []string{
		"--runner-timeout",
		"--stream",
		"runner-logs/agent.events.jsonl",
		"runner-logs/opencode/<task-id>.jsonl",
		"runner-logs/opencode/<task-id>.stderr.log",
		".yolo-runner/clones/<task-id>/runner-logs/opencode/<task-id>.jsonl",
		"session.prompt .* exiting loop",
		"session.idle publishing",
		"service=permission",
		"permission=question",
		"opencode stall category=",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("runbook missing %q", needle)
		}
	}
}

func TestHangRunbookDefinesClassificationBuckets(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	runbookPath := filepath.Join(repoRoot, "docs", "hang-triage.md")
	contents, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read runbook: %v", err)
	}

	runbook := string(contents)
	for _, bucket := range []string{"idle-transport-open", "permission-question", "no-output-stall", "other"} {
		if !strings.Contains(runbook, bucket) {
			t.Fatalf("runbook missing classification bucket %q", bucket)
		}
	}
}
