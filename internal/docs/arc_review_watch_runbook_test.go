package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArcReviewWatchRunbookDocumentsSafeStartupAndReset(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	runbookPath := filepath.Join(repoRoot, "docs", "arc-review-watch.md")
	contents, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read arc review watch runbook: %v", err)
	}

	runbook := string(contents)
	required := []string{
		"arc_review_watch:",
		"poll_interval: 30s",
		"lock_path: .yolo-runner/arc-review-watch.lock",
		"state_path: .yolo-runner/arc-review-watch-state.json",
		"allow_ship: false",
		"./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run",
		"--events \"runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl\" --stream | ./bin/yolo-tui --events-stdin",
		"SQLite",
		"sqlite3 \"$STATE\"",
		"rm -f .yolo-runner/arc-review-watch.lock",
		"mv .yolo-runner/arc-review-watch-state.json \".yolo-runner/arc-review-watch-state.json.$(date +%Y%m%d_%H%M%S).bak\"",
		"arc-pr-review-runner",
		"kill <pid>",
		"runner-logs/arc-pr-review-<session-id>.log",
		"runner-logs/arc-review-watch.events.jsonl",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook missing %q", needle)
		}
	}
}

func TestReadmeLinksArcReviewWatchRunbook(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	if !strings.Contains(string(contents), "docs/arc-review-watch.md") {
		t.Fatalf("README missing arc review watch runbook link")
	}
}
