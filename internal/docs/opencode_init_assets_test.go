package docs

import (
	"strings"
	"testing"
)

func TestReadmeDocumentsTaskSplittingSkillInstallAndUsage(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	required := []string{
		"task-splitting skill",
		".opencode/skills/task-splitting/SKILL.md",
		".opencode/commands/split-tasks.md",
		".opencode/commands/split-tasks-strict.md",
		"/split-tasks",
		"/split-tasks-strict",
		"yolo-runner init --repo <repo>",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing skill/init guidance %q", needle)
		}
	}
}

func TestInstallMatrixMentionsInitReminderAfterScriptInstall(t *testing.T) {
	matrix := readRepoFile(t, "docs", "install-matrix.md")
	if !strings.Contains(matrix, "script prints `yolo-runner init --repo <repo>` reminder") {
		t.Fatalf("install matrix missing init reminder after install script runs")
	}
}
