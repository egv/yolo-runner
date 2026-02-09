package opencode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindSerenaInitErrorSinceDetectsRealSerenaInitFailure(t *testing.T) {
	tempDir := t.TempDir()
	stderrPath := filepath.Join(tempDir, "run.stderr.log")
	content := "INFO service=mcp key=serena mcp stderr: language server manager is not initialized\n"
	if err := os.WriteFile(stderrPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write stderr file: %v", err)
	}

	line, ok := findSerenaInitErrorSince(stderrPath, time.Time{})
	if !ok {
		t.Fatalf("expected serena init failure to be detected")
	}
	if line == "" {
		t.Fatalf("expected non-empty matching line")
	}
}

func TestFindSerenaInitErrorSinceIgnoresMarkerInsideAgentToolOutput(t *testing.T) {
	tempDir := t.TempDir()
	stderrPath := filepath.Join(tempDir, "run.stderr.log")
	content := "INFO service=acp-agent event={\"part\":{\"type\":\"tool\",\"output\":\"language server manager is not initialized\"}}\n"
	if err := os.WriteFile(stderrPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write stderr file: %v", err)
	}

	_, ok := findSerenaInitErrorSince(stderrPath, time.Time{})
	if ok {
		t.Fatalf("expected false positive marker in tool output to be ignored")
	}
}

func TestFindSerenaInitErrorSinceIgnoresAnyACPAgentEventLine(t *testing.T) {
	tempDir := t.TempDir()
	stderrPath := filepath.Join(tempDir, "run.stderr.log")
	content := "INFO service=acp-agent event=language server manager is not initialized\n"
	if err := os.WriteFile(stderrPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write stderr file: %v", err)
	}

	_, ok := findSerenaInitErrorSince(stderrPath, time.Time{})
	if ok {
		t.Fatalf("expected acp-agent event line to be ignored")
	}
}
