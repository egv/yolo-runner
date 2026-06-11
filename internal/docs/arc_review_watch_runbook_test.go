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
		"poll_interval: 30s",
		"lock_path: .yolo-runner/arc-review-watch.lock",
		"state_path: .yolo-runner/arc-review-watch-state.json",
		"reviewer: alice",
		"max_concurrency: 1",
		"allow_ship: false",
		"Keep `allow_ship: false` for the first production run",
		"- `reviewer`: optional reviewer login used to filter discovered PRs",
		"./bin/yolo-agent config validate --repo .",
		"./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run",
		"--events \"runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl\" --stream | ./bin/yolo-tui --events-stdin",
		"runner-logs/arc-review-watch.events.jsonl",
		"runner-logs/arc-pr-review-<session-id>.log",
		"## Live Review Cycle",
		"A non-dry polling iteration discovers eligible open PRs",
		"The child `arc-pr-review-runner` fetches PR runtime state",
		"`review`: run the configured model, post inline comments and a summary, then store the reviewed revision.",
		"`answer`: run the configured model and post replies for unanswered PR comments.",
		"`ship`: call the ship gate only after `allow_ship` is true",
		"SQLite",
		"sqlite3 \"$STATE\"",
		"update pr_sessions set status = 'crashed', pid = 0",
		"select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;",
		"mv .yolo-runner/arc-review-watch-state.json \".yolo-runner/arc-review-watch-state.json.$(date +%Y%m%d_%H%M%S).bak\"",
		"pgrep -af 'yolo-agent arc-review-watch|arc-pr-review-runner'",
		"kill <pid>",
		"pkill -f 'arc-pr-review-runner'",
		"rm -f .yolo-runner/arc-review-watch.lock",
		"arc_review_watch.allow_ship",
		"allow_ship: true",
		"--allow-ship=true",
		"The watcher hands `arc_review_watch.allow_ship` to the child runner as `--allow-ship=true` or `--allow-ship=false`",
		"The child runner also receives `--reviewer <login>` when `arc_review_watch.reviewer` is set",
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
