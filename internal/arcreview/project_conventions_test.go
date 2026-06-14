package arcreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadProjectConventionsReadsBothFilesAndBoundsOutput(t *testing.T) {
	workspace := t.TempDir()
	projectRoot := "taxi/backend-cpp/services/ai_minion"
	rootPath := filepath.Join(workspace, filepath.FromSlash(projectRoot))
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}

	longAgents := strings.Repeat("prefer table-driven tests\n", 1000) + "tail marker"
	writeProjectConventionsTestFile(t, rootPath, "CLAUDE.md", "\nUse arcadia style.\n\n")
	writeProjectConventionsTestFile(t, rootPath, "AGENTS.md", "\n"+longAgents+"\n")

	got, err := ReadProjectConventions(workspace, projectRoot)
	if err != nil {
		t.Fatalf("ReadProjectConventions() error = %v", err)
	}

	for _, want := range []string{
		"CLAUDE.md:",
		"Use arcadia style.",
		"AGENTS.md:",
		"prefer table-driven tests",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected conventions to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n\nUse arcadia style.\n\n") {
		t.Fatalf("expected file contents to be trimmed, got:\n%s", got)
	}
	if len(got) > MaxProjectConventionsBytes {
		t.Fatalf("expected conventions length <= %d, got %d", MaxProjectConventionsBytes, len(got))
	}
	if strings.Contains(got, "tail marker") {
		t.Fatalf("expected conventions to be truncated before tail marker, got length %d", len(got))
	}

	absent, err := ReadProjectConventions(workspace, "taxi/backend-cpp/services/other")
	if err != nil {
		t.Fatalf("ReadProjectConventions() absent error = %v", err)
	}
	if absent != "" {
		t.Fatalf("ReadProjectConventions() absent = %q, want empty", absent)
	}
}

func writeProjectConventionsTestFile(t *testing.T, dir string, name string, contents string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
