package docs

import (
	"strings"
	"testing"
)

func TestWatchSupervisorPlaybookDocumentsOperatorWorkflow(t *testing.T) {
	runbook := readRepoFile(t, "docs", "watch-supervisor.md")

	required := []string{
		"Watch Supervisor Playbook",
		"`yolo-agent watch`",
		"starts every configured source in-process",
		"autoscale tick",
		"limits.max_concurrent",
		"git push",
		"queues:",
		"preset: adapta",
		"type: arcpr",
		"runner_pools:",
		"min_replicas: 1",
		"max_replicas: 4",
		"capacity: 1",
		"autoscale:",
		"default_mode: stream",
		"./bin/yolo-agent watch \\",
		"--tui",
		"--stream",
		"Manual Split-Process Fallback",
		"./bin/yolo-agent source startrek",
		"./bin/yolo-agent source arcpr",
		"./bin/yolo-agent events follow --since 1h | ./bin/yolo-tui --events-stdin",
		"Recovery",
		"Do not clear `.yolo-runner/scheduler-state.json`",
		"go test ./...",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("watch supervisor playbook missing %q", needle)
		}
	}
}

func TestWatchSupervisorPlaybookDocumentsBRWatchRunbook(t *testing.T) {
	runbook := readRepoFile(t, "docs", "watch-supervisor.md")

	required := []string{
		"## br Watch Runbook",
		"watcher -> sink -> runner",
		"Workspace-wide br watch",
		"br --no-daemon ready --limit 0 --json",
		"Root-scoped br watch",
		"root: yolo-epic",
		"Missing `.beads`",
		"br init",
		"watch.sources[].repo",
		"Manual br start",
		"./bin/yolo-agent source br \\",
		"--root yolo-epic",
		"Monitor br watch",
		"./bin/yolo-agent events follow --since 1h | ./bin/yolo-tui --events-stdin",
		"Stop br watch",
		"press Ctrl-C",
		"Recovery for br watch",
		"br update <task-id> --status open",
		"br sync --flush-only",
		"Do not clear `.yolo-runner/scheduler-state.json`",
	}
	for _, needle := range required {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("watch supervisor br runbook missing %q", needle)
		}
	}
}

func TestReadmeLinksWatchSupervisorPlaybook(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	if !strings.Contains(readme, "docs/watch-supervisor.md") {
		t.Fatalf("README missing watch supervisor playbook link")
	}
}
