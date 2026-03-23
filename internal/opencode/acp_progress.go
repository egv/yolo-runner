package opencode

import (
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	acp "github.com/ironpark/acp-go"
)

// NormalizeACPProgressNotification maps an ACP session notification directly
// to a shared RunnerProgress event without going through a text intermediate.
// Returns (RunnerProgress, true) when the notification produces a progress event,
// or (zero, false) when the notification should be skipped.
func NormalizeACPProgressNotification(notification *acp.SessionNotification) (contracts.RunnerProgress, bool) {
	if notification == nil {
		return contracts.RunnerProgress{}, false
	}

	update := &notification.Update
	sessionID := strings.TrimSpace(string(notification.SessionId))
	now := time.Now().UTC()

	if toolCall := update.GetToolcall(); toolCall != nil {
		progressType := toolCallProgressType(toolCall.Status)
		message := strings.TrimSpace(toolCall.Title)
		metadata := toolCallMetadata(sessionID, string(toolCall.ToolCallId), toolCall.Kind, toolCall.Status)
		return contracts.RunnerProgress{
			Type:      string(progressType),
			Message:   message,
			Metadata:  metadata,
			Timestamp: now,
		}, true
	}

	if toolUpdate := update.GetToolcallupdate(); toolUpdate != nil {
		progressType := toolCallProgressType(toolUpdate.Status)
		message := strings.TrimSpace(toolUpdate.Title)
		metadata := toolCallMetadata(sessionID, string(toolUpdate.ToolCallId), toolUpdate.Kind, toolUpdate.Status)
		return contracts.RunnerProgress{
			Type:      string(progressType),
			Message:   message,
			Metadata:  metadata,
			Timestamp: now,
		}, true
	}

	if message := update.GetAgentmessagechunk(); message != nil {
		text := ""
		if message.Content.IsText() {
			text = message.Content.GetText().Text
		}
		if text == "" {
			return contracts.RunnerProgress{}, false
		}
		return contracts.RunnerProgress{
			Type:      string(contracts.EventTypeRunnerOutput),
			Message:   text,
			Metadata:  sessionMetadata(sessionID),
			Timestamp: now,
		}, true
	}

	if thought := update.GetAgentthoughtchunk(); thought != nil {
		text := ""
		if thought.Content.IsText() {
			text = thought.Content.GetText().Text
		}
		if text == "" {
			return contracts.RunnerProgress{}, false
		}
		return contracts.RunnerProgress{
			Type:      string(contracts.EventTypeRunnerOutput),
			Message:   text,
			Metadata:  sessionMetadata(sessionID),
			Timestamp: now,
		}, true
	}

	if update.GetPlan() != nil {
		return contracts.RunnerProgress{
			Type:      string(contracts.EventTypeRunnerProgress),
			Message:   "plan",
			Metadata:  sessionMetadata(sessionID),
			Timestamp: now,
		}, true
	}

	return contracts.RunnerProgress{}, false
}

func toolCallProgressType(status *acp.ToolCallStatus) contracts.EventType {
	if status == nil {
		return contracts.EventTypeRunnerCommandStarted
	}
	switch *status {
	case acp.ToolCallStatusCompleted, acp.ToolCallStatusFailed:
		return contracts.EventTypeRunnerCommandFinished
	default:
		return contracts.EventTypeRunnerCommandStarted
	}
}

func toolCallMetadata(sessionID string, toolCallID string, kind *acp.ToolKind, status *acp.ToolCallStatus) map[string]string {
	metadata := map[string]string{}
	if sessionID != "" {
		metadata["session_id"] = sessionID
	}
	if toolCallID != "" {
		metadata["tool_call_id"] = toolCallID
	}
	if kind != nil {
		if k := strings.TrimSpace(string(*kind)); k != "" {
			metadata["kind"] = k
		}
	}
	if status != nil {
		if s := strings.TrimSpace(string(*status)); s != "" {
			metadata["status"] = s
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func sessionMetadata(sessionID string) map[string]string {
	if sessionID == "" {
		return nil
	}
	return map[string]string{"session_id": sessionID}
}
