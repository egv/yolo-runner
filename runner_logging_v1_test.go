package yolo_runner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	filePath := filepath.Join(repoRoot, "README.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.txt")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	return repoRoot
}

func readSummaryEntries(t *testing.T, logPath string) []map[string]string {
	t.Helper()
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	entries := make([]map[string]string, 0, len(lines))
	for _, line := range lines {
		entry := map[string]string{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid json line: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestLogRunnerSummaryAppendsEntries(t *testing.T) {
	repoRoot := initGitRepo(t)
	commitSHA := runGit(t, repoRoot, "rev-parse", "HEAD")

	if err := logRunnerSummary(repoRoot, "issue-1", "First", "completed", ""); err != nil {
		t.Fatalf("logRunnerSummary error: %v", err)
	}
	if err := logRunnerSummary(repoRoot, "issue-2", "Second", "blocked", ""); err != nil {
		t.Fatalf("logRunnerSummary error: %v", err)
	}

	logPath := filepath.Join(repoRoot, "runner-logs", "beads_yolo_runner.jsonl")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}

	entries := readSummaryEntries(t, logPath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}

	first := entries[0]
	if first["issue_id"] != "issue-1" {
		t.Fatalf("expected issue_id issue-1, got %q", first["issue_id"])
	}
	if first["title"] != "First" {
		t.Fatalf("expected title First, got %q", first["title"])
	}
	if first["status"] != "completed" {
		t.Fatalf("expected status completed, got %q", first["status"])
	}
	if first["commit_sha"] != commitSHA {
		t.Fatalf("expected commit sha %q, got %q", commitSHA, first["commit_sha"])
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", first["timestamp"]); err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q", first["timestamp"])
	}

	second := entries[1]
	if second["status"] != "blocked" {
		t.Fatalf("expected status blocked, got %q", second["status"])
	}
	if second["commit_sha"] != commitSHA {
		t.Fatalf("expected commit sha %q, got %q", commitSHA, second["commit_sha"])
	}
}
