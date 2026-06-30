package opencode

import (
	"context"
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
		progressType := toolCallProgressType(toolCall.Kind)
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
		progressType := toolCallProgressType(toolUpdate.Kind)
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
			Type:      string(contracts.EventTypeAgentText),
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
			Type:      string(contracts.EventTypeAgentText),
			Message:   text,
			Metadata:  sessionMetadata(sessionID),
			Timestamp: now,
		}, true
	}

	if update.GetPlan() != nil {
		return contracts.RunnerProgress{
			Type:      string(contracts.EventTypeAgentProgress),
			Message:   "plan",
			Metadata:  sessionMetadata(sessionID),
			Timestamp: now,
		}, true
	}

	return contracts.RunnerProgress{}, false
}

func toolCallProgressType(kind *acp.ToolKind) contracts.EventType {
	if kind != nil && strings.EqualFold(string(*kind), string(acp.ToolKindExecute)) {
		return contracts.EventTypeCommandRun
	}
	return contracts.EventTypeToolInvoked
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

type acpProgressCallbackTarget interface {
	getEventSink() contracts.TaskSessionEventSink
	setEventSink(contracts.TaskSessionEventSink)
	installUpdateHandler(func(*acp.SessionNotification)) func()
}

func (c *acpClient) installUpdateHandler(handler func(*acp.SessionNotification)) func() {
	if c == nil {
		return func() {}
	}
	previous := c.onUpdate
	c.onUpdate = func(notification *acp.SessionNotification) {
		if previous != nil {
			previous(notification)
		}
		if handler != nil {
			handler(notification)
		}
	}
	return func() {
		c.onUpdate = previous
	}
}

func installACPProgressCallbacks(client ACPClient, onProgress func(contracts.RunnerProgress)) func() {
	if client == nil || onProgress == nil {
		return func() {}
	}
	target, ok := client.(acpProgressCallbackTarget)
	if !ok {
		return func() {}
	}

	restoreUpdateHandler := target.installUpdateHandler(func(notification *acp.SessionNotification) {
		if progress, ok := NormalizeACPProgressNotification(notification); ok {
			onProgress(progress)
		}
	})

	previousSink := target.getEventSink()
	target.setEventSink(contracts.TaskSessionEventSinkFunc(func(ctx context.Context, event contracts.TaskSessionEvent) error {
		if previousSink != nil {
			if err := previousSink.HandleEvent(ctx, event); err != nil {
				return err
			}
		}
		if progress, ok := NormalizeOpencodeTaskSessionEvent(event); ok {
			onProgress(progress)
		}
		return nil
	}))

	return func() {
		target.setEventSink(previousSink)
		restoreUpdateHandler()
	}
}

func NormalizeOpencodeTaskSessionEvent(event contracts.TaskSessionEvent) (contracts.RunnerProgress, bool) {
	progress, ok := contracts.NormalizeTaskSessionEvent(event)
	if !ok {
		return contracts.RunnerProgress{}, false
	}
	if event.Type == contracts.TaskSessionEventTypeApprovalRequired {
		if event.Approval == nil || event.Approval.Decision == nil {
			progress.Type = string(contracts.EventTypeAgentProgress)
			if progress.Metadata != nil && progress.Metadata["reason"] == string(event.Type) {
				delete(progress.Metadata, "reason")
			}
			return progress, true
		}
		if strings.TrimSpace(string(event.Approval.Decision.Outcome)) != "" {
			if progress.Metadata == nil {
				progress.Metadata = map[string]string{}
			}
			progress.Metadata["outcome"] = string(event.Approval.Decision.Outcome)
		}
		switch event.Approval.Decision.Outcome {
		case contracts.TaskSessionApprovalRejected, contracts.TaskSessionApprovalDeferred:
		default:
			progress.Type = string(contracts.EventTypeAgentProgress)
			if progress.Metadata != nil && progress.Metadata["reason"] == string(event.Type) {
				delete(progress.Metadata, "reason")
			}
			if strings.TrimSpace(event.Approval.Decision.Reason) != "" {
				if progress.Metadata == nil {
					progress.Metadata = map[string]string{}
				}
				progress.Metadata["decision_reason"] = event.Approval.Decision.Reason
			}
			return progress, true
		}
		if progress.Metadata == nil {
			progress.Metadata = map[string]string{}
		}
		progress.Type = string(contracts.EventTypeAgentBlocked)
		progress.Metadata["reason"] = string(contracts.BlockReasonPermissionDenied)
		if progress.Metadata["detail"] == "" && event.Approval != nil {
			progress.Metadata["detail"] = firstNonEmptyString(
				event.Approval.Request.Message,
				event.Approval.Request.Title,
				event.Approval.Request.ID,
			)
		}
	}
	return progress, true
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// NormalizeACPPromptResponse maps an ACP PromptResponse completion signal
// to a shared RunnerProgress event. Returns (RunnerProgress, true) for known
// stop reasons, or (zero, false) for nil, empty, or unrecognized stop reasons.
func NormalizeACPPromptResponse(resp *acp.PromptResponse) (contracts.RunnerProgress, bool) {
	if resp == nil {
		return contracts.RunnerProgress{}, false
	}
	stopReason := strings.TrimSpace(string(resp.StopReason))
	if stopReason == "" {
		return contracts.RunnerProgress{}, false
	}

	var progressType contracts.EventType
	switch resp.StopReason {
	case acp.StopReasonEndTurn:
		progressType = contracts.EventTypeAgentFinished
	case acp.StopReasonMaxTokens, acp.StopReasonMaxTurnRequests, acp.StopReasonRefusal, acp.StopReasonCancelled:
		progressType = contracts.EventTypeRunnerWarning
	default:
		return contracts.RunnerProgress{}, false
	}

	return contracts.RunnerProgress{
		Type:      string(progressType),
		Message:   stopReason,
		Metadata:  map[string]string{"stop_reason": stopReason},
		Timestamp: time.Now().UTC(),
	}, true
}
