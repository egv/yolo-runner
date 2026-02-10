package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadmeDocumentsRequiredGUILibrariesAndArchitecture(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	readme := string(contents)
	for _, expected := range []string{"Bubble Tea", "Bubbles", "Lip Gloss", "Elm-style"} {
		if !strings.Contains(readme, expected) {
			t.Fatalf("README missing GUI requirement: %s", expected)
		}
	}
}

func TestMigrationIncludesStdinGUIOperatorFlow(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	migrationPath := filepath.Join(repoRoot, "MIGRATION.md")
	contents, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration doc: %v", err)
	}

	migration := string(contents)
	if !strings.Contains(migration, "yolo-agent --stream") || !strings.Contains(migration, "yolo-tui --events-stdin") {
		t.Fatalf("MIGRATION missing stdin GUI flow command")
	}
	if !strings.Contains(migration, "decode_error") {
		t.Fatalf("MIGRATION missing decode fallback guidance")
	}
}

func TestGUIRunbookExistsWithProductionChecklist(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	runbookPath := filepath.Join(repoRoot, "docs", "GUI_RUNBOOK.md")
	contents, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read GUI runbook: %v", err)
	}

	runbook := string(contents)
	for _, expected := range []string{"Production Runbook", "Preflight", "Failure Handling", "yolo-agent --stream"} {
		if !strings.Contains(runbook, expected) {
			t.Fatalf("GUI runbook missing %q", expected)
		}
	}
}
