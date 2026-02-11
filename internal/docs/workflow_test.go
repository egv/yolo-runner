package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadmeMentionsCloseEligible(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	if !strings.Contains(string(contents), "bd epic close-eligible") {
		t.Fatalf("README missing bd epic close-eligible step")
	}
}

func TestReadmeDocumentsRunnerTimeoutProfiles(t *testing.T) {
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
	if !strings.Contains(readme, "--runner-timeout") {
		t.Fatalf("README missing --runner-timeout guidance")
	}
	if !strings.Contains(readme, "Local profile") {
		t.Fatalf("README missing local timeout profile")
	}
	if !strings.Contains(readme, "CI profile") {
		t.Fatalf("README missing CI timeout profile")
	}
	if !strings.Contains(readme, "Long-task profile") {
		t.Fatalf("README missing long-task timeout profile")
	}
	if !strings.Contains(readme, "0s") {
		t.Fatalf("README missing default timeout behavior")
	}
	if !strings.Contains(readme, "10m") {
		t.Fatalf("README missing local timeout recommendation")
	}
	if !strings.Contains(readme, "20m") {
		t.Fatalf("README missing CI timeout recommendation")
	}
	if !strings.Contains(readme, "45m") {
		t.Fatalf("README missing long-task timeout recommendation")
	}
}
