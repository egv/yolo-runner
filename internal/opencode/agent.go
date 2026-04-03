package opencode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentRelativePath              = ".opencode/agent/yolo.md"
	agentSourceRelativePath        = "yolo.md"
	releaseAgentRelativePath       = ".opencode/agent/release.md"
	releaseAgentSourceRelativePath = "agent/release.md"
	taskSplittingSkillRelativePath = ".opencode/skills/task-splitting/SKILL.md"
	taskSplittingSkillSourcePath   = "skills/task-splitting/SKILL.md"
	splitTasksCommandRelativePath  = ".opencode/commands/split-tasks.md"
	splitTasksCommandSourcePath    = "commands/split-tasks.md"
	splitTasksStrictCommandPath    = ".opencode/commands/split-tasks-strict.md"
	splitTasksStrictSourcePath     = "commands/split-tasks-strict.md"
)

var (
	ErrAgentMissing         = errors.New("yolo agent missing")
	ErrAgentPermissionUnset = errors.New("yolo agent missing permission allow")
)

func ValidateAgent(repoRoot string) error {
	agentPath := filepath.Join(repoRoot, agentRelativePath)
	content, err := os.ReadFile(agentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("missing yolo agent file at %s", agentRelativePath)
		}
		return err
	}
	if !hasPermissionAllow(content) {
		return fmt.Errorf("yolo agent missing permission: allow; install .opencode/agent/yolo.md and bundled .opencode skills/commands into this repo")
	}
	return nil
}

func InitAgent(repoRoot string) error {
	for _, item := range []struct {
		src      string
		dst      string
		required bool
	}{
		{agentSourceRelativePath, agentRelativePath, true},
		{releaseAgentSourceRelativePath, releaseAgentRelativePath, false},
		{taskSplittingSkillSourcePath, taskSplittingSkillRelativePath, true},
		{splitTasksCommandSourcePath, splitTasksCommandRelativePath, true},
		{splitTasksStrictSourcePath, splitTasksStrictCommandPath, true},
	} {
		sourcePath := filepath.Join(repoRoot, item.src)
		sourceContent, err := os.ReadFile(sourcePath)
		if os.IsNotExist(err) {
			if item.required {
				return fmt.Errorf("read yolo agent template: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("read agent template from %s: %w", item.src, err)
		}
		destinationPath := filepath.Join(repoRoot, item.dst)
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return fmt.Errorf("create agent dir: %w", err)
		}
		if err := os.WriteFile(destinationPath, sourceContent, 0o644); err != nil {
			return fmt.Errorf("write agent file: %w", err)
		}
	}

	return nil
}

func hasPermissionAllow(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "permission: allow" {
			return true
		}
	}
	return false
}
