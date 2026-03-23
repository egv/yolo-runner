package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendACPRequestWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "runner-logs", "opencode", "issue-1.jsonl")
	if err := AppendACPRequest(logPath, ACPRequestEntry{
		IssueID:     "issue-1",
		RequestType: "permission",
		Decision:    "allow",
	}); err != nil {
		t.Fatalf("append error: %v", err)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if err := ValidateStructuredLogLine([]byte(line)); err != nil {
			t.Fatalf("logged entry should conform to schema: %v", err)
		}
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		t.Fatalf("expected newline-terminated jsonl")
	}
}

func TestAppendACPRequestIncludesReasonAndContext(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "reason-context.jsonl")
	if err := AppendACPRequest(logPath, ACPRequestEntry{
		IssueID:     "issue-1",
		RequestType: "permission",
		Decision:    "allow",
		Message:     "repo.write",
		Reason:      "tool_use",
		Context:     "read /tmp/file.txt",
	}); err != nil {
		t.Fatalf("append error: %v", err)
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	entry := map[string]string{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(content))), &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if entry["reason"] != "tool_use" {
		t.Fatalf("expected reason=tool_use, got %q", entry["reason"])
	}
	if entry["context"] != "read /tmp/file.txt" {
		t.Fatalf("expected context, got %q", entry["context"])
	}
}
