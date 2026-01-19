package opencode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	agentRelativePath = ".opencode/agent/yolo.md"
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

func hasPermissionAllow(content []byte) bool {
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "permission: allow" {
			return true
		}
	}
	return false
}
