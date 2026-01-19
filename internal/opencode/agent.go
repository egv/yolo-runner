package opencode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentRelativePath       = ".opencode/agent/yolo.md"
	agentSourceRelativePath = "yolo.md"
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
		return fmt.Errorf("yolo agent missing permission: allow; run opencode init")
	}
	return nil
}

func InitAgent(repoRoot string) error {
	sourcePath := filepath.Join(repoRoot, agentSourceRelativePath)
	sourceContent, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read yolo agent template: %w", err)
	}
	destinationPath := filepath.Join(repoRoot, agentRelativePath)
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	if err := os.WriteFile(destinationPath, sourceContent, 0o644); err != nil {
		return fmt.Errorf("write agent file: %w", err)
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
