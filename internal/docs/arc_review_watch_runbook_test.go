package docs

import (
	"strings"
	"testing"
)

func TestArcReviewWatchRunbookDocumentsOperatorWorkflow(t *testing.T) {
	runbook := readRepoFile(t, "docs", "arc-review-watch.md")

	required := []string{
		"Arc Review Watch Operator Runbook",
		"arc_review_watch:",
		"poll_interval:",
		"lock_path:",
		"state_path:",
		"max_concurrency:",
		"allow_ship: false",
		"Keep `allow_ship: false` for the first production run",
		"./bin/yolo-agent arc-review-watch --repo . --profile arc-dev --once --dry-run",
		"--events \"runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl\" --stream | ./bin/yolo-tui --events-stdin",
		"runner-logs/arc-review-watch.events.jsonl",
		"runner-logs/arc-pr-review-<session-id>.log",
		"sqlite3 .yolo-runner/arc-review-watch-state.json",
		"UPDATE pr_sessions SET status = 'crashed', pid = 0",
		"rm -f .yolo-runner/arc-review-watch.lock",
		"pkill -f 'arc-pr-review-runner'",
		"arc_review_watch.allow_ship: true",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook missing %q", needle)
		}
	}
}

func TestReadmeLinksArcReviewWatchRunbook(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	if !strings.Contains(readme, "docs/arc-review-watch.md") {
		t.Fatalf("README missing arc review watch runbook link")
	}
}
