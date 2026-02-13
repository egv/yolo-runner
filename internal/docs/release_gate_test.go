package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, elems ...string) string {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	path := filepath.Join(append([]string{repoRoot}, elems...)...)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(elems...), err)
	}
	return string(contents)
}

func TestMakefileDefinesE8ReleaseGateChecklistTarget(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	required := []string{
		"release-gate-e8:",
		"go test ./cmd/yolo-agent",
		"CodexTKConcurrency2LandsViaMergeQueue",
		"ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage",
		"KimiLinearProfileProcessesAndClosesIssue",
		"GitHubProfileProcessesAndClosesIssue",
		"go test ./internal/docs",
	}
	for _, needle := range required {
		if !strings.Contains(makefile, needle) {
			t.Fatalf("Makefile release gate missing %q", needle)
		}
	}
}

func TestReadmeDocumentsE8ReleaseGateChecklist(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	required := []string{
		"E8 Release Gate Checklist",
		"make release-gate-e8",
		"TestE2E_CodexTKConcurrency2LandsViaMergeQueue",
		"TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage",
		"TestE2E_KimiLinearProfileProcessesAndClosesIssue",
		"TestE2E_GitHubProfileProcessesAndClosesIssue",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README release gate checklist missing %q", needle)
		}
	}
}

func TestMigrationDocumentsE8ReleaseGateMigrationInstructions(t *testing.T) {
	migration := readRepoFile(t, "MIGRATION.md")

	required := []string{
		"Release Gate (E8) Migration",
		"yolo-agent --repo . --root <tk-root-id> --agent-backend codex",
		"--concurrency 2",
		"--agent-backend claude",
		"--agent-backend kimi",
		"--profile linear-kimi-demo",
		"--profile github-demo",
		"make release-gate-e8",
	}
	for _, needle := range required {
		if !strings.Contains(migration, needle) {
			t.Fatalf("MIGRATION release gate section missing %q", needle)
		}
	}
}
