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
	if !strings.Contains(strings.ToLower(err.Error()), ".opencode/agent/yolo.md") {
		t.Fatalf("expected guidance to mention repo-local opencode assets, got %q", err.Error())
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

func TestInitAgentCopiesAgentSkillAndCommandTemplates(t *testing.T) {
	repoRoot := t.TempDir()

	rootYolo := filepath.Join(repoRoot, "yolo.md")
	if err := os.WriteFile(rootYolo, []byte("root yolo template"), 0o644); err != nil {
		t.Fatalf("write root yolo template: %v", err)
	}

	releaseTemplate := filepath.Join(repoRoot, "agent", "release.md")
	if err := os.MkdirAll(filepath.Dir(releaseTemplate), 0o755); err != nil {
		t.Fatalf("mkdir release template dir: %v", err)
	}
	if err := os.WriteFile(releaseTemplate, []byte("release skill template"), 0o644); err != nil {
		t.Fatalf("write release template: %v", err)
	}

	skillTemplate := filepath.Join(repoRoot, "skills", "task-splitting", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillTemplate), 0o755); err != nil {
		t.Fatalf("mkdir task-splitting skill dir: %v", err)
	}
	if err := os.WriteFile(skillTemplate, []byte("task splitting skill template"), 0o644); err != nil {
		t.Fatalf("write task-splitting skill template: %v", err)
	}

	commandDir := filepath.Join(repoRoot, "commands")
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("mkdir command dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "split-tasks.md"), []byte("split tasks command template"), 0o644); err != nil {
		t.Fatalf("write split-tasks command template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(commandDir, "split-tasks-strict.md"), []byte("split tasks strict command template"), 0o644); err != nil {
		t.Fatalf("write split-tasks-strict command template: %v", err)
	}

	err := InitAgent(repoRoot)
	if err != nil {
		t.Fatalf("expected init agent to succeed: %v", err)
	}

	yoloPath := filepath.Join(repoRoot, ".opencode", "agent", "yolo.md")
	yoloContent, err := os.ReadFile(yoloPath)
	if err != nil {
		t.Fatalf("read generated yolo agent: %v", err)
	}
	if string(yoloContent) != "root yolo template" {
		t.Fatalf("expected yolo agent template content to be copied, got %q", string(yoloContent))
	}

	releasePath := filepath.Join(repoRoot, ".opencode", "agent", "release.md")
	releaseContent, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatalf("read generated release skill: %v", err)
	}
	if string(releaseContent) != "release skill template" {
		t.Fatalf("expected release skill content to be copied, got %q", string(releaseContent))
	}

	skillPath := filepath.Join(repoRoot, ".opencode", "skills", "task-splitting", "SKILL.md")
	skillContent, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read generated task-splitting skill: %v", err)
	}
	if string(skillContent) != "task splitting skill template" {
		t.Fatalf("expected task-splitting skill content to be copied, got %q", string(skillContent))
	}

	splitTasksPath := filepath.Join(repoRoot, ".opencode", "commands", "split-tasks.md")
	splitTasksContent, err := os.ReadFile(splitTasksPath)
	if err != nil {
		t.Fatalf("read generated split-tasks command: %v", err)
	}
	if string(splitTasksContent) != "split tasks command template" {
		t.Fatalf("expected split-tasks command content to be copied, got %q", string(splitTasksContent))
	}

	splitTasksStrictPath := filepath.Join(repoRoot, ".opencode", "commands", "split-tasks-strict.md")
	splitTasksStrictContent, err := os.ReadFile(splitTasksStrictPath)
	if err != nil {
		t.Fatalf("read generated split-tasks-strict command: %v", err)
	}
	if string(splitTasksStrictContent) != "split tasks strict command template" {
		t.Fatalf("expected split-tasks-strict command content to be copied, got %q", string(splitTasksStrictContent))
	}
}

func TestInitAgentRequiresMainTemplate(t *testing.T) {
	repoRoot := t.TempDir()
	releaseTemplate := filepath.Join(repoRoot, "agent", "release.md")
	if err := os.MkdirAll(filepath.Dir(releaseTemplate), 0o755); err != nil {
		t.Fatalf("mkdir release template dir: %v", err)
	}
	if err := os.WriteFile(releaseTemplate, []byte("release skill template"), 0o644); err != nil {
		t.Fatalf("write release template: %v", err)
	}

	err := InitAgent(repoRoot)
	if err == nil {
		t.Fatalf("expected init to fail without yolo.md")
	}
	if !strings.Contains(err.Error(), "read yolo agent template") {
		t.Fatalf("expected error to mention missing yolo agent template, got %q", err.Error())
	}
}
