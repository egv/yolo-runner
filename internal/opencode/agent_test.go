package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAgentMissingFile(t *testing.T) {
	repoRoot := t.TempDir()

	err := ValidateAgent(repoRoot)

	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "yolo.md") {
		t.Fatalf("expected error to mention yolo.md, got %q", err.Error())
	}
}

func TestValidateAgentMissingPermission(t *testing.T) {
	repoRoot := t.TempDir()
	agentDir := filepath.Join(repoRoot, ".opencode", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	agentPath := filepath.Join(agentDir, "yolo.md")
	if err := os.WriteFile(agentPath, []byte("---\nname: yolo\n---\n"), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	err := ValidateAgent(repoRoot)

	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "permission: allow") {
		t.Fatalf("expected error to mention permission allow, got %q", err.Error())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "yolo-runner init") {
		t.Fatalf("expected guidance to run yolo-runner init, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), ".opencode/agent/yolo.md") {
		t.Fatalf("expected error to mention agent file path, got %q", err.Error())
	}
}

func TestValidateAgentAllowsPermissionAllow(t *testing.T) {
	repoRoot := t.TempDir()
	agentDir := filepath.Join(repoRoot, ".opencode", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	agentPath := filepath.Join(agentDir, "yolo.md")
	content := "---\nname: yolo\npermission: allow\n---\n"
	if err := os.WriteFile(agentPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}

	err := ValidateAgent(repoRoot)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
