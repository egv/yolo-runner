package codex

import (
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type AppServerCompletion struct {
	Reason           string
	ReviewReady      bool
	HasReviewVerdict bool
	Artifacts        map[string]string
	Metadata         map[string]string
}

func NormalizeAppServerNotification(message contracts.JSONRPCMessage, mode contracts.RunnerMode) (contracts.TaskSessionEvent, *AppServerCompletion, bool) {
	method := strings.TrimSpace(message.Method)
	if method == "" {
		return contracts.TaskSessionEvent{}, nil, false
	}
	params := message.Params
	threadID := lookupString(params, "threadId", "thread_id")
	turnID := lookupString(params, "turnId", "turn_id")
	itemID := lookupString(params, "itemId", "item_id")
	item := lookupMap(params, "item")
	if itemID == "" {
		itemID = lookupString(item, "id", "itemId", "item_id")
	}
	itemType := normalizeItemType(lookupString(item, "type", "itemType", "item_type"))
	if itemType == "" {
		itemType = deriveItemTypeFromMethod(method)
	}

	metadata := cloneStringMap(nil)
	metadata = setMetadataValue(metadata, "thread_id", threadID)
	metadata = setMetadataValue(metadata, "turn_id", turnID)
	metadata = setMetadataValue(metadata, "item_id", itemID)
	metadata = setMetadataValue(metadata, "item_type", itemType)

	event := contracts.TaskSessionEvent{
		SessionID: threadID,
		Timestamp: time.Now().UTC(),
		Metadata:  metadata,
	}

	switch method {
	case "thread/started":
		event.Type = contracts.TaskSessionEventTypeLifecycle
		event.Message = "thread started"
		event.Lifecycle = &contracts.TaskSessionLifecycleEvent{State: contracts.TaskSessionLifecycleReady}
		return event, nil, true
	case "turn/started":
		event.Type = contracts.TaskSessionEventTypeLifecycle
		event.Message = "turn started"
		event.Lifecycle = &contracts.TaskSessionLifecycleEvent{State: contracts.TaskSessionLifecycleRunning}
		return event, nil, true
	case "turn/completed":
		reason := lookupString(params, "stopReason", "stop_reason", "reason")
		metadata = setMetadataValue(metadata, "reason", reason)
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventTypeLifecycle
		event.Message = "turn completed"
		event.Lifecycle = &contracts.TaskSessionLifecycleEvent{State: contracts.TaskSessionLifecycleStopped}
		completion := &AppServerCompletion{
			Reason:    reason,
			Artifacts: map[string]string{},
			Metadata:  cloneStringMap(metadata),
		}
		if mode == contracts.RunnerModeReview {
			if verdict, ok := extractReviewVerdict(params); ok {
				completion.HasReviewVerdict = true
				completion.Artifacts["review_verdict"] = verdict
				completion.ReviewReady = strings.EqualFold(verdict, "pass")
				if strings.EqualFold(verdict, "fail") {
					if feedback, ok := extractReviewFailFeedback(params); ok {
						completion.Artifacts["review_fail_feedback"] = feedback
					}
				}
			}
		}
		if len(completion.Artifacts) == 0 {
			completion.Artifacts = nil
		}
		return event, completion, true
	case "item/commandExecution/requestApproval":
		command := stringSlice(lookupSlice(params, "command"))
		approvalID := lookupString(params, "id", "approvalId", "approval_id")
		metadata = setMetadataValue(metadata, "approval_id", approvalID)
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventTypeApprovalRequired
		event.Message = coalesceMessage(
			lookupString(params, "reason", "message"),
			lookupString(params, "title"),
			"approval required",
		)
		event.Approval = &contracts.TaskSessionApprovalEvent{
			Request: contracts.TaskSessionApprovalRequest{
				ID:       approvalID,
				Kind:     contracts.TaskSessionApprovalKindCommand,
				Title:    lookupString(params, "title"),
				Message:  lookupString(params, "reason", "message"),
				Command:  command,
				Metadata: cloneStringMap(metadata),
				Payload:  params,
			},
		}
		return event, nil, true
	case "item/fileChange/requestApproval":
		approvalID := lookupString(params, "id", "approvalId", "approval_id")
		metadata = setMetadataValue(metadata, "approval_id", approvalID)
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventTypeApprovalRequired
		event.Message = coalesceMessage(
			lookupString(params, "reason", "message"),
			lookupString(params, "title"),
			"file change approval required",
		)
		event.Approval = &contracts.TaskSessionApprovalEvent{
			Request: contracts.TaskSessionApprovalRequest{
				ID:       approvalID,
				Kind:     contracts.TaskSessionApprovalKindCommand,
				Title:    lookupString(params, "title"),
				Message:  lookupString(params, "reason", "message"),
				Metadata: cloneStringMap(metadata),
				Payload:  params,
			},
		}
		return event, nil, true
	case "item/tool/requestUserInput":
		event.Type = contracts.TaskSessionEventTypeApprovalRequired
		event.Message = coalesceMessage(
			extractText(lookupSlice(params, "questions")),
			toolRequestUserInputTitle(params),
			"tool input required",
		)
		event.Approval = &contracts.TaskSessionApprovalEvent{
			Request: contracts.TaskSessionApprovalRequest{
				ID:       lookupString(params, "itemId", "item_id"),
				Kind:     contracts.TaskSessionApprovalKindCommand,
				Title:    toolRequestUserInputTitle(params),
				Message:  extractText(lookupSlice(params, "questions")),
				Metadata: cloneStringMap(metadata),
				Payload:  params,
			},
		}
		return event, nil, true
	}

	if strings.HasSuffix(method, "/delta") {
		event.Type = contracts.TaskSessionEventTypeOutput
		event.Message = coalesceMessage(
			lookupString(params, "delta", "text", "message"),
			extractText(lookupMap(params, "delta")),
		)
		if strings.TrimSpace(event.Message) == "" {
			return contracts.TaskSessionEvent{}, nil, false
		}
		return event, nil, true
	}

	if strings.HasPrefix(method, "item/") || strings.HasPrefix(method, "tool/") {
		event.Type = contracts.TaskSessionEventTypeProgress
		event.Message = coalesceMessage(
			lookupString(item, "title", "name"),
			lookupString(params, "title", "name"),
			defaultProgressMessage(method, itemType),
		)
		event.Progress = &contracts.TaskSessionProgressEvent{Phase: itemType}
		return event, nil, true
	}

	return contracts.TaskSessionEvent{}, nil, false
}

func RunnerProgressFromAppServerNotification(message contracts.JSONRPCMessage, mode contracts.RunnerMode) (contracts.RunnerProgress, *AppServerCompletion, bool) {
	event, completion, ok := NormalizeAppServerNotification(message, mode)
	if !ok {
		return contracts.RunnerProgress{}, nil, false
	}
	progress, ok := contracts.NormalizeTaskSessionEvent(event)
	if !ok {
		return contracts.RunnerProgress{}, nil, false
	}
	return progress, completion, true
}

func ApplyAppServerCompletion(result *contracts.RunnerResult, completion *AppServerCompletion) {
	if result == nil || completion == nil {
		return
	}
	if strings.TrimSpace(completion.Reason) != "" && strings.TrimSpace(result.Reason) == "" {
		result.Reason = completion.Reason
	}
	if len(completion.Artifacts) > 0 {
		if result.Artifacts == nil {
			result.Artifacts = map[string]string{}
		}
		for key, value := range completion.Artifacts {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				continue
			}
			result.Artifacts[key] = value
		}
	}
	if completion.HasReviewVerdict {
		result.ReviewReady = completion.ReviewReady
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func lookupString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			switch typed := value.(type) {
			case string:
				if trimmed := strings.TrimSpace(typed); trimmed != "" {
					return trimmed
				}
			case fmt.Stringer:
				if trimmed := strings.TrimSpace(typed.String()); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}

func setMetadataValue(dst map[string]string, key string, value string) map[string]string {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return dst
	}
	if dst == nil {
		dst = map[string]string{}
	}
	dst[key] = value
	return dst
}

func lookupMap(data map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if raw, ok := data[key]; ok {
			if mapped, ok := raw.(map[string]any); ok {
				return mapped
			}
		}
	}
	return nil
}

func lookupSlice(data map[string]any, keys ...string) []any {
	for _, key := range keys {
		if raw, ok := data[key]; ok {
			if values, ok := raw.([]any); ok {
				return values
			}
		}
	}
	return nil
}

func stringSlice(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

func coalesceMessage(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeItemType(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return strings.ReplaceAll(trimmed, " ", "_")
}

func deriveItemTypeFromMethod(method string) string {
	parts := strings.Split(strings.TrimSpace(method), "/")
	if len(parts) < 2 {
		return ""
	}
	switch parts[0] {
	case "tool":
		return "tool"
	case "item":
		if len(parts) >= 3 && parts[2] == "delta" {
			return normalizeItemType(parts[1])
		}
	}
	return normalizeItemType(parts[len(parts)-1])
}

func defaultProgressMessage(method string, itemType string) string {
	method = strings.TrimSpace(method)
	itemType = strings.TrimSpace(strings.ReplaceAll(itemType, "_", " "))
	switch {
	case strings.HasSuffix(method, "/started"):
		if itemType != "" {
			return itemType + " started"
		}
	case strings.HasSuffix(method, "/completed"):
		if itemType != "" {
			return itemType + " completed"
		}
	}
	if itemType != "" {
		return itemType
	}
	return method
}

func extractReviewVerdict(params map[string]any) (string, bool) {
	for _, key := range []string{"reviewVerdict", "review_verdict", "verdict"} {
		if verdict := strings.ToLower(lookupString(params, key)); verdict == "pass" || verdict == "fail" {
			return verdict, true
		}
	}
	if verdict, ok := lastStructuredVerdictLine(extractText(params)); ok {
		return verdict, true
	}
	return "", false
}

func extractReviewFailFeedback(params map[string]any) (string, bool) {
	for _, key := range []string{"reviewFailFeedback", "review_fail_feedback", "feedback"} {
		if feedback := lookupString(params, key); feedback != "" {
			return feedback, true
		}
	}
	if feedback, ok := lastStructuredReviewFailFeedbackLine(extractText(params)); ok {
		return feedback, true
	}
	return "", false
}

func extractText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		texts := []string{
			lookupString(typed, "text", "message", "title", "reason"),
		}
		if nested := lookupMap(typed, "output", "item", "delta"); nested != nil {
			texts = append(texts, extractText(nested))
		}
		if values := lookupSlice(typed, "content", "items"); len(values) > 0 {
			for _, value := range values {
				texts = append(texts, extractText(value))
			}
		}
		return strings.TrimSpace(strings.Join(filterEmpty(texts), "\n"))
	case []any:
		texts := make([]string, 0, len(typed))
		for _, item := range typed {
			texts = append(texts, extractText(item))
		}
		return strings.TrimSpace(strings.Join(filterEmpty(texts), "\n"))
	default:
		return ""
	}
}

func filterEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func toolRequestUserInputTitle(params map[string]any) string {
	for _, question := range lookupSlice(params, "questions") {
		mapped, ok := question.(map[string]any)
		if !ok {
			continue
		}
		if title := coalesceMessage(lookupString(mapped, "header"), lookupString(mapped, "question")); title != "" {
			return title
		}
	}
	return ""
}
