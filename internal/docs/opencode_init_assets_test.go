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
		"Repo-local OpenCode Assets",
		".opencode/agent/yolo.md",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing skill/init guidance %q", needle)
		}
	}
}

func TestInstallMatrixMentionsInitReminderAfterScriptInstall(t *testing.T) {
	matrix := readRepoFile(t, "docs", "install-matrix.md")
	if !strings.Contains(matrix, "script prints a repo-local OpenCode asset reminder") {
		t.Fatalf("install matrix missing init reminder after install script runs")
	}
}
