package codingagents

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultAgentHealthTimeout = 2 * time.Second

func parseAgentHealthTimeout(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAgentHealthTimeout, nil
	}
	value, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid health timeout %q: %w", raw, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("health timeout must be greater than 0, got %s", value)
	}
	return value, nil
}

func parseAgentHealthInterval(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 100 * time.Millisecond, nil
	}
	value, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid health interval %q: %w", raw, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("health interval must be greater than 0, got %s", value)
	}
	return value, nil
}

func runAgentHealthCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
