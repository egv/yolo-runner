package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadmeDocumentsDistributedBusFallbackBackend(t *testing.T) {
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
	required := []string{
		"defaults to `redis`",
		"--distributed-bus-backend` is omitted",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing distributed bus fallback backend documentation: %q", needle)
		}
	}
}

func TestReadmeDocumentsDistributedOperatorNotes(t *testing.T) {
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
	required := []string{
		"Startup",
		"Cancellation",
		"Teardown",
		"in-flight tasks",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing distributed operator note: %q", needle)
		}
	}
}
