package linear

import (
	"fmt"
	"strings"
)

// ReconstructPromptContext builds deterministic prompt context from immutable
// agent activities only. Mutable comment fields are intentionally ignored.
func ReconstructPromptContext(event AgentSessionEvent, priorActivities []AgentActivity) string {
	base := strings.TrimSpace(event.AgentSession.PromptContext)

	activities := make([]AgentActivity, 0, len(priorActivities)+1)
	activities = append(activities, priorActivities...)
	if event.AgentActivity != nil {
		activities = append(activities, *event.AgentActivity)
	}

	lines := make([]string, 0, len(activities))
	for _, activity := range activities {
		line := strings.TrimSpace(formatActivityLine(activity.Content))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return base
	}

	parts := make([]string, 0, len(lines)+2)
	if base != "" {
		parts = append(parts, base)
	}
	parts = append(parts, "Conversation activities:")
	for i, line := range lines {
		parts = append(parts, fmt.Sprintf("%d. %s", i+1, line))
	}
	return strings.Join(parts, "\n")
}

func formatActivityLine(content AgentActivityContent) string {
	body := strings.TrimSpace(content.Body)
	switch content.Type {
	case AgentActivityContentTypeThought:
		if body == "" {
			return ""
		}
		return "thought: " + body
	case AgentActivityContentTypePrompt:
		if body == "" {
			return ""
		}
		return "prompt: " + body
	case AgentActivityContentTypeElicitation:
		if body == "" {
			return ""
		}
		return "elicitation: " + body
	case AgentActivityContentTypeResponse:
		if body == "" {
			return ""
		}
		return "response: " + body
	case AgentActivityContentTypeError:
		if body == "" {
			return ""
		}
		return "error: " + body
	case AgentActivityContentTypeAction:
		action := strings.TrimSpace(content.Action)
		parameter := strings.TrimSpace(content.Parameter)
		if action == "" || parameter == "" {
			return ""
		}
		result := ""
		if content.Result != nil {
			result = strings.TrimSpace(*content.Result)
		}
		if result == "" {
			return fmt.Sprintf("action: %s | parameter: %s", action, parameter)
		}
		return fmt.Sprintf("action: %s | parameter: %s | result: %s", action, parameter, result)
	default:
		if body == "" {
			return ""
		}
		return string(content.Type) + ": " + body
	}
}
