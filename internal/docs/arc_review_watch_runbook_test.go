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
		"reconciles the result into `pr_sessions`",
		"The current watcher does not start children from newly created `pending` sessions directly.",
		"The watcher starts replacement `arc-pr-review-runner` children with `--once`",
		"The `--once` child writes one heartbeat for the target session and exits",
		"It does not fetch PR runtime state, read `reviewed_revisions`, post review comments, answer comments, or ship.",
		"Replacement children are handed to `arc-pr-review-runner` with `--session-id`, `--state-path`, `--events`, `--once`, and `--allow-ship=true` or `--allow-ship=false`.",
		"The child runner also receives `--reviewer <login>` when `arc_review_watch.reviewer` is set",
		"The full `arc-pr-review-runner` loop only runs when the runner is started without `--once`.",
		"In non-`--once` live review mode, the child writes a heartbeat, fetches PR runtime state",
		"reads the last handled revision from `reviewed_revisions`",
		"plans exactly one action",
		"`review`: run the configured model, post inline comments and a summary, then store the current revision in `reviewed_revisions`.",
		"`answer`: run the configured model, post replies for unanswered PR comments, then record answered comment IDs.",
		"`ship`: call the ship gate when planning reaches a ship candidate. The gate still evaluates `allow_ship`, the reviewed current revision, open blockers, unanswered comments, check status, and model ship approval.",
		"`wait`: leave the PR untouched when it is terminal, checks are still pending or unknown, or shipping remains disabled after review.",
		"SQLite",
		"sqlite3 \"$STATE\"",
		"update pr_sessions set status = 'crashed', pid = 0",
		"select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;",
		"`reviewed_revisions` is read before planning and written after a successful `review` action",
		"mv .yolo-runner/arc-review-watch-state.json \".yolo-runner/arc-review-watch-state.json.$(date +%Y%m%d_%H%M%S).bak\"",
		"pgrep -af 'yolo-agent arc-review-watch|arc-pr-review-runner'",
		"kill <pid>",
		"pkill -f 'arc-pr-review-runner'",
		"rm -f .yolo-runner/arc-review-watch.lock",
		"arc_review_watch.allow_ship",
		"allow_ship: true",
		"--allow-ship=true",
		"The watcher hands `arc_review_watch.allow_ship` to the child runner as `--allow-ship=true` or `--allow-ship=false`",
		"For watcher-started handoff, `allow_ship` is visible in child arguments but cannot trigger review, answer, or ship because the `--once` child exits after the heartbeat.",
		"Manual full-runner invocations do not read `arc_review_watch.allow_ship`; pass the intended gate explicitly with `--allow-ship=true` or `--allow-ship=false`.",
		"It only affects shipping when `arc-pr-review-runner` is running the full non-`--once` review loop.",
		"First production run: keep `allow_ship: false` and watch at least one heartbeat-only watcher cycle",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook missing %q", needle)
		}
	}

	forbidden := []string{
		"Eligible sessions are handed to `arc-pr-review-runner` with `--session-id`, `--state-path`, `--events`, and `--allow-ship=true` or `--allow-ship=false`.",
		"After a non-terminal action, the runner waits for `poll_interval` and repeats until the PR reaches a terminal state or the process is stopped:",
		"With `allow_ship: false`, the runner may still review and answer comments, but the ship gate reports shipping disabled.",
		"With `allow_ship: true`, shipping can proceed only after the same runner has reviewed the current revision and all other gate conditions pass.",
		"First production run: keep `allow_ship: false` and watch at least one full review cycle.",
		"First production run: keep `allow_ship: false` and watch at least one live watcher cycle",
	}
	for _, needle := range forbidden {
		if strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook still contains stale behavior %q", needle)
		}
	}
}

func TestReadmeLinksArcReviewWatchRunbook(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	if !strings.Contains(readme, "docs/arc-review-watch.md") {
		t.Fatalf("README missing arc review watch runbook link")
	}
}
