package yolo_runner

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestMakefileTargets(t *testing.T) {
	content, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("expected Makefile to exist: %v", err)
	}

	makefile := string(content)
	if !regexp.MustCompile(`(?m)^test:\n\tgo test \./\.\.\.`).MatchString(makefile) {
		t.Fatalf("expected make test to run go test ./...\nMakefile:\n%s", makefile)
	}
	if !regexp.MustCompile(`(?m)^build:(?:\n\t.*)*\n\tgo build .*bin/yolo-runner`).MatchString(makefile) {
		t.Fatalf("expected make build to build bin/yolo-runner\nMakefile:\n%s", makefile)
	}
}

func TestReadmeDocumentsBuildAndUsage(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	requirements := []string{"bd", "opencode", "git", "uv"}
	for _, requirement := range requirements {
		if !strings.Contains(readme, requirement) {
			t.Fatalf("expected README to mention %q", requirement)
		}
	}

	examples := []string{"--model", "--dry-run"}
	for _, example := range examples {
		if !strings.Contains(readme, example) {
			t.Fatalf("expected README to include example with %q", example)
		}
	}
}
