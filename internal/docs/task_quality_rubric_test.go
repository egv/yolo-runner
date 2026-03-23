package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskQualityRubricDocumentsRequiredFieldsAndClarityRules(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	rubricPath := filepath.Join(repoRoot, "docs", "task-quality-rubric.md")
	contents, err := os.ReadFile(rubricPath)
	if err != nil {
		t.Fatalf("read task quality rubric: %v", err)
	}

	rubric := string(contents)
	required := []string{
		"## Required Fields (must be present)",
		"`Title`:",
		"`Acceptance Criteria`:",
		"`Deliverables`:",
		"`Testing Plan`:",
		"## Clarity Rules (quality gates)",
		"## Scoring",
	}
	for _, needle := range required {
		if !strings.Contains(rubric, needle) {
			t.Fatalf("rubric missing required section/field: %q", needle)
		}
	}
}

func TestTaskQualityRubricIncludesGoodAndBadScoringExamples(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	rubricPath := filepath.Join(repoRoot, "docs", "task-quality-rubric.md")
	contents, err := os.ReadFile(rubricPath)
	if err != nil {
		t.Fatalf("read task quality rubric: %v", err)
	}

	rubric := string(contents)
	for _, needle := range []string{
		"## Sample Applications",
		"### Good Example",
		"### Bad Example",
		"**Expected score:** `93/100`",
		"**Expected score:** `28/100`",
	} {
		if !strings.Contains(rubric, needle) {
			t.Fatalf("rubric missing expected scoring example coverage: %q", needle)
		}
	}
}
