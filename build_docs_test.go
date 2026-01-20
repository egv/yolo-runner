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

func TestReadmeDocumentsOpenCodeConfigIsolation(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	requirements := []string{
		"XDG_CONFIG_HOME=~/.config/opencode-runner",
		"override",
		"inspect",
	}
	for _, requirement := range requirements {
		if !strings.Contains(readme, requirement) {
			t.Fatalf("expected README to mention %q", requirement)
		}
	}
}

func TestReadmeDocumentsSmokeTest(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	requiredPhrases := []string{
		"Manual Smoke Test",
		"throwaway branch",
		"worktree",
		"`bd ready`",
		"runner-logs/beads_yolo_runner.jsonl",
		"runner-logs/opencode/",
		"Success looks like",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Fatalf("expected README smoke test instructions to mention %q", phrase)
		}
	}

	if !regexp.MustCompile(`(?i)inspect.*commit`).MatchString(readme) {
		t.Fatalf("expected README smoke test instructions to mention inspecting the resulting commit")
	}
}

func TestReadmeDocumentsTUIAndHeadless(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	requiredPhrases := []string{
		"TUI",
		"TTY",
		"--headless",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Fatalf("expected README to mention %q", phrase)
		}
	}

	if !regexp.MustCompile(`(?i)default.*tui`).MatchString(readme) {
		t.Fatalf("expected README to describe the default TUI behavior")
	}
	if !regexp.MustCompile(`(?i)headless`).MatchString(readme) {
		t.Fatalf("expected README to describe the headless mode")
	}
}

func TestReadmeDocumentsInitAndAgentTroubleshooting(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	requiredPhrases := []string{
		"yolo-runner init",
		"agent installation",
		"missing agent",
		"refuses to start",
		"Troubleshooting",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Fatalf("expected README to mention %q", phrase)
		}
	}

	if !regexp.MustCompile(`(?i)init.*usage`).MatchString(readme) {
		t.Fatalf("expected README to describe init usage")
	}
	if !regexp.MustCompile(`(?i)recover`).MatchString(readme) {
		t.Fatalf("expected README to mention recovery steps")
	}
}

func TestReadmeDocumentsMoleculeSelectionSemantics(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	if !regexp.MustCompile(`(?i)container\s+types.*epic.*molecule`).MatchString(readme) {
		t.Fatalf("expected README to describe container types epic and molecule")
	}
	if !regexp.MustCompile(`(?i)traversable.*open.*in_progress`).MatchString(readme) {
		t.Fatalf("expected README to describe traversable statuses open and in_progress")
	}
	if !regexp.MustCompile(`(?i)leaf.*open\s+only`).MatchString(readme) {
		t.Fatalf("expected README to describe leaf eligibility as open only")
	}
}

func TestReadmeDocumentsConsoleOutputAndTroubleshooting(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("expected README.md to exist: %v", err)
	}

	readme := string(content)
	requiredPhrases := []string{
		"Sample output",
		"tail -f runner-logs/opencode/opencode.log",
		"current task",
	}
	for _, phrase := range requiredPhrases {
		if !strings.Contains(readme, phrase) {
			t.Fatalf("expected README to mention %q", phrase)
		}
	}

	if !regexp.MustCompile("```[\\s\\S]*yolo-runner[\\s\\S]*```").MatchString(readme) {
		t.Fatalf("expected README to include a sample output snippet with yolo-runner")
	}
	if !regexp.MustCompile(`(?i)task.*bd show`).MatchString(readme) {
		t.Fatalf("expected README to explain how to identify the current task")
	}
}
