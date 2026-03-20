package opencode

import (
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type TaskSessionRuntime struct {
	binary  string
	command []string
}

func NewTaskSessionRuntime(binary string, command ...string) *TaskSessionRuntime {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = defaultBinary
	}
	return &TaskSessionRuntime{
		binary:  resolvedBinary,
		command: append([]string(nil), command...),
	}
}

func (r *TaskSessionRuntime) buildCommand(request contracts.TaskSessionStartRequest) []string {
	if len(request.Command) > 0 {
		return normalizeServeCommand(strings.TrimSpace(r.binary), request.Command)
	}
	if len(r.command) > 0 {
		return normalizeServeCommand(strings.TrimSpace(r.binary), r.command)
	}

	command := BuildServeArgs()
	if len(command) > 0 && strings.TrimSpace(r.binary) != "" {
		command[0] = r.binary
	}
	return command
}

func normalizeServeCommand(binary string, command []string) []string {
	normalized := append([]string(nil), command...)
	if len(normalized) == 0 {
		return normalized
	}
	if strings.TrimSpace(binary) != "" && strings.TrimSpace(normalized[0]) == "serve" {
		normalized = append([]string{binary}, normalized...)
	} else if len(normalized) > 1 && strings.TrimSpace(binary) != "" && strings.TrimSpace(normalized[0]) == defaultBinary && strings.TrimSpace(normalized[1]) == "serve" {
		normalized[0] = binary
	}
	if !isServeCommand(normalized) || hasServeHostname(normalized) {
		return normalized
	}
	return append(normalized, "--hostname", serveLoopbackHostname)
}

func isServeCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}
	if strings.TrimSpace(command[0]) == "serve" {
		return true
	}
	return len(command) > 1 && strings.TrimSpace(command[1]) == "serve"
}

func hasServeHostname(command []string) bool {
	for _, arg := range command {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "--hostname" || strings.HasPrefix(trimmed, "--hostname=") {
			return true
		}
	}
	return false
}
