package docs

import (
	"strings"
	"testing"
)

func TestArcReviewWatchRunbookDocumentsOperatorWorkflow(t *testing.T) {
	runbook := readRepoFile(t, "docs", "arc-review-watch.md")

	required := []string{
		"Arc Review Watch Deprecation Runbook",
		"`yolo-agent arc-review-watch` is a compatibility shim.",
		"delegates to `yolo-agent source arcpr`",
		"arc_review_watch:",
		"poll_interval: 30s",
		"state_path: .yolo-runner/arcpr-source-state.db",
		"reviewer: alice",
		"allow_ship: false",
		"objects_base_dir: ~/.yolo-runner/pr-objects",
		"mounts_base_dir: ~/.yolo-runner/pr-mounts",
		"`reviewer`: required reviewer identity used by PR discovery. Omitted or blank discovers no eligible PRs.",
		"`objects_base_dir`: base directory for per-PR arc object stores. Omit it to use `~/.yolo-runner/pr-objects`.",
		"`mounts_base_dir`: base directory for per-PR arc mount checkouts. Omit it to use `~/.yolo-runner/pr-mounts`.",
		"./bin/yolo-agent config validate --repo .",
		"./bin/yolo-agent source arcpr --repo . --profile arc-review --queue .yolo-runner/queue.db --once",
		"--events \"runner-logs/source-arcpr-$(date +%Y%m%d_%H%M%S).events.jsonl\" --stream | ./bin/yolo-tui --events-stdin",
		"./bin/yolo-agent arc-review-watch --repo . --profile arc-review --once",
		"./bin/yolo-agent runner --queue .yolo-runner/queue.db --presets arc-review",
		"`--dry-run` flag is accepted by `arc-review-watch` for command compatibility",
		"SQLite",
		"sqlite3 \"$STATE\"",
		"select pr_id, revision, reviewed_at from reviewed_revisions order by reviewed_at desc;",
		"select pr_id, comment_id, answered_at from answered_comments order by answered_at desc;",
		"delete from reviewed_revisions where pr_id = '<pr-id>';",
		"delete from answered_comments where pr_id = '<pr-id>';",
		"Queue leases replace the old PR session table and child review process lifecycle.",
		"There is no `pr_sessions` table, no stale-session restart, and no `arc-pr-review-runner` command.",
		"arc_review_watch.allow_ship",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook missing %q", needle)
		}
	}

	removed := []string{
		"lock_path:",
		"workspaces:",
		"branches:",
		"arc mount --list --json",
		"arc pr list --json --reviewer <login> --status open",
		"arc pr list --json --author <login> --status open",
		"`lock_path`",
		"`workspaces`",
		"`branches`",
	}
	for _, needle := range removed {
		if strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook still documents removed field %q", needle)
		}
	}
}

func TestArcReviewDocsDescribeCrossProjectPRReviewModel(t *testing.T) {
	runbook := readRepoFile(t, "docs", "arc-review-watch.md")
	presets := readRepoFile(t, "docs", "environment-presets.md")

	runbookRequired := []string{
		"API-backed",
		"Arcanum public API",
		"/api/v1/public/review-requests?status=open&reviewer=<login>",
		"/api/v1/public/review-requests?status=open&author=<login>",
		"No mounted Arc workspace is needed for discovery",
		"Each PR review work item receives an isolated checkout",
		"auto-detects the project root from changed files",
		"PR review does not configure or require MCP servers",
		"`allow_ship` defaults to `false`",
	}
	for _, needle := range runbookRequired {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("arc review watch runbook missing cross-project PR model detail %q", needle)
		}
	}

	presetRequired := []string{
		"PR review project auto-detection",
		"the preset selects runner capacity and agent settings",
		"per-PR isolated checkout",
		"PR review execution does not use MCP",
	}
	for _, needle := range presetRequired {
		if !strings.Contains(presets, needle) {
			t.Fatalf("environment presets docs missing PR review model detail %q", needle)
		}
	}
}

func TestReadmeLinksArcReviewWatchRunbook(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	if !strings.Contains(readme, "docs/arc-review-watch.md") {
		t.Fatalf("README missing arc review watch runbook link")
	}
}
