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
		"`reviewer`: required reviewer identity used by PR discovery. Omitted or blank discovers no eligible PRs.",
		"./bin/yolo-agent config validate --repo .",
		"./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once --dry-run",
		"--events \"runner-logs/arc-review-watch-$(date +%Y%m%d_%H%M%S).events.jsonl\" --stream | ./bin/yolo-tui --events-stdin",
		"runner-logs/arc-review-watch.events.jsonl",
		"runner-logs/arc-pr-review-<session-id>.log",
		"## Watcher Handoff And Live Review Cycle",
		"The watcher polls configured `workspaces`, filters discovered PRs by `reviewer`, `branches`, and open status",
		"Newly discovered PRs are recorded with their workspace, branch, revision, and `pending` status.",
		"Watcher-started child processes currently include `--once`, so they write one heartbeat and exit instead of running the full live review loop.",
		"Run `arc-pr-review-runner` without `--once` for a full live review cycle.",
		"Each live cycle writes a heartbeat",
		"fetches the current PR runtime state",
		"store the reviewed revision in `reviewed_revisions`",
		"Answer: for unanswered comments, run the reply path and post answers.",
		"Ship: call the ship gate only after `allow_ship: true`",
		"SQLite",
		"sqlite3 \"$STATE\"",
		"select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;",
		"update pr_sessions set status = 'crashed', pid = 0",
		"delete from reviewed_revisions where pr_id = '<pr-id>';",
		"mv .yolo-runner/arc-review-watch-state.json \".yolo-runner/arc-review-watch-state.json.$(date +%Y%m%d_%H%M%S).bak\"",
		"pgrep -af 'yolo-agent arc-review-watch|arc-pr-review-runner'",
		"kill <pid>",
		"pkill -f 'arc-pr-review-runner'",
		"rm -f .yolo-runner/arc-review-watch.lock",
		"passes the setting to child runners as `--allow-ship=<true|false>`",
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
