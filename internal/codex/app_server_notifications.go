package codex

import (
	"encoding/json"
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
	itemID := lookupString(params, "itemId", "item_id", "targetItemId", "target_item_id")
	item := lookupMap(params, "item")
	if itemID == "" {
		itemID = lookupString(item, "id", "itemId", "item_id")
	}
	itemType := normalizeItemType(lookupString(item, "type", "itemType", "item_type"))
	if itemType == "" && method == "item/autoApprovalReview/completed" {
		itemType = "autoApprovalReview"
	}
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
		metadata = setMetadataValue(metadata, "completion_json", marshalCompletionJSON(params))
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
	case "error":
		errorInfo := lookupMap(params, "error")
		if errorInfo == nil {
			errorInfo = params
		}
		codexErrorInfo := lookupAny(errorInfo, "codexErrorInfo", "codex_error_info")
		detail := coalesceMessage(extractText(errorInfo), extractText(params), "codex error")
		metadata = setMetadataValue(metadata, "reason", string(blockReasonFromCodexError(errorInfo)))
		metadata = setMetadataValue(metadata, "detail", detail)
		metadata = setMetadataValue(metadata, "error_type", coalesceMessage(lookupString(errorInfo, "type", "code"), codexErrorInfoName(codexErrorInfo)))
		metadata = setMetadataValue(metadata, "codex_error_info", codexErrorInfoName(codexErrorInfo))
		metadata = setMetadataValue(metadata, "http_status_code", codexErrorInfoHTTPStatus(codexErrorInfo))
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventType(contracts.EventTypeAgentBlocked)
		event.Message = detail
		return event, nil, true
	case "thread/tokenUsage/updated":
		usage := normalizeCodexTokenUsage(params)
		metadata = setMetadataValue(metadata, "input_tokens", usage["input_tokens"])
		metadata = setMetadataValue(metadata, "output_tokens", usage["output_tokens"])
		metadata = setMetadataValue(metadata, "total_tokens", usage["total_tokens"])
		if len(metadata) == 0 {
			return contracts.TaskSessionEvent{}, nil, false
		}
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventType(contracts.EventTypeTokenUsage)
		event.Message = "token usage updated"
		return event, nil, true
	case "item/commandExecution/requestApproval":
		command := commandApprovalParts(params)
		approvalID := approvalRequestID(message, params)
		metadata = setMetadataValue(metadata, "approval_id", approvalID)
		metadata = setMetadataValue(metadata, "reason", string(contracts.BlockReasonPermissionDenied))
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
		approvalID := approvalRequestID(message, params)
		metadata = setMetadataValue(metadata, "approval_id", approvalID)
		metadata = setMetadataValue(metadata, "reason", string(contracts.BlockReasonPermissionDenied))
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

	if isAutoApprovalReviewDenied(method, itemType, params, item) && strings.HasSuffix(method, "/completed") {
		review := autoApprovalReview(params, item)
		action := guardianApprovalReviewAction(params, item)
		detail := coalesceMessage(
			lookupString(review, "rationale", "reason", "message", "details", "detail"),
			lookupString(params, "reason", "message", "title"),
			lookupString(item, "reason", "message", "title"),
			"auto approval denied",
		)
		metadata = setMetadataValue(metadata, "reason", string(contracts.BlockReasonPermissionDenied))
		metadata = setMetadataValue(metadata, "detail", detail)
		metadata = setMetadataValue(metadata, "review_status", lookupString(review, "status"))
		metadata = setMetadataValue(metadata, "review_id", lookupString(params, "reviewId", "review_id"))
		metadata = setMetadataValue(metadata, "decision_source", lookupString(params, "decisionSource", "decision_source"))
		metadata = setMetadataValue(metadata, "risk_level", lookupString(review, "riskLevel", "risk_level"))
		metadata = setMetadataValue(metadata, "user_authorization", lookupString(review, "userAuthorization", "user_authorization"))
		metadata = setMetadataValue(metadata, "action", legacyGuardianActionValue(params))
		metadata = setMetadataValue(metadata, "action_type", guardianActionType(params, action))
		metadata = setMetadataValue(metadata, "target_item_id", lookupString(params, "targetItemId", "target_item_id"))
		metadata = setMetadataValue(metadata, "command", coalesceMessage(guardianActionCommand(action), commandString(params), commandString(item)))
		metadata = setMetadataValue(metadata, "program", lookupString(action, "program"))
		metadata = setMetadataValue(metadata, "cwd", coalesceMessage(lookupString(action, "cwd"), lookupString(params, "cwd"), lookupString(item, "cwd")))
		metadata = setMetadataValue(metadata, "permission_reason", lookupString(action, "reason"))
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventType(contracts.EventTypeAgentBlocked)
		event.Message = detail
		return event, nil, true
	}

	if isCommandExecutionItem(itemType) && strings.HasSuffix(method, "/completed") {
		command := commandString(item)
		metadata = setMetadataValue(metadata, "command", command)
		metadata = setMetadataValue(metadata, "cwd", lookupString(item, "cwd"))
		metadata = setMetadataValue(metadata, "exit_code", anyString(lookupAny(item, "exitCode", "exit_code")))
		metadata = setMetadataValue(metadata, "duration_ms", anyString(lookupAny(item, "durationMs", "duration_ms")))
		metadata = setMetadataValue(metadata, "outcome", coalesceMessage(lookupString(item, "status", "outcome"), commandOutcome(item)))
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventType(contracts.EventTypeCommandRun)
		event.Message = coalesceMessage(command, lookupString(item, "title", "name"), "command completed")
		return event, nil, true
	}

	if isToolItem(itemType) && strings.HasSuffix(method, "/completed") {
		metadata = setMetadataValue(metadata, "tool", itemType)
		metadata = setMetadataValue(metadata, "target", toolTarget(item))
		metadata = setMetadataValue(metadata, "outcome", coalesceMessage(lookupString(item, "status", "outcome"), "completed"))
		event.Metadata = metadata
		event.Type = contracts.TaskSessionEventType(contracts.EventTypeToolInvoked)
		event.Message = coalesceMessage(lookupString(item, "title", "name"), toolTarget(item), itemType)
		return event, nil, true
	}

	if strings.HasSuffix(method, "/delta") {
		event.Type = contracts.TaskSessionEventTypeOutput
		if raw, ok := lookupRawString(params, "delta", "text", "message"); ok {
			event.Message = raw
		} else {
			event.Message = extractText(lookupMap(params, "delta"))
		}
		if event.Message == "" || (strings.TrimSpace(event.Message) == "" && !hasRawString(params, "delta", "text", "message")) {
			return contracts.TaskSessionEvent{}, nil, false
		}
		metadata = setMetadataValue(metadata, "preserve_whitespace", "true")
		event.Metadata = metadata
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
	if reason := strings.TrimSpace(event.Metadata["reason"]); reason != "" && event.Type == contracts.TaskSessionEventTypeApprovalRequired {
		if progress.Metadata == nil {
			progress.Metadata = map[string]string{}
		}
		progress.Metadata["reason"] = reason
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

func mergeAppServerStreamCompletion(target *AppServerCompletion, message contracts.JSONRPCMessage, mode contracts.RunnerMode) {
	if target == nil || mode != contracts.RunnerModeReview {
		return
	}
	if verdict, ok := extractReviewVerdict(message.Params); ok {
		if target.Artifacts == nil {
			target.Artifacts = map[string]string{}
		}
		target.HasReviewVerdict = true
		target.ReviewReady = strings.EqualFold(verdict, "pass")
		target.Artifacts["review_verdict"] = verdict
	}
	if feedback, ok := extractReviewFailFeedback(message.Params); ok {
		if target.Artifacts == nil {
			target.Artifacts = map[string]string{}
		}
		target.Artifacts["review_fail_feedback"] = feedback
	}
}

func mergeAppServerCompletion(target *AppServerCompletion, fallback *AppServerCompletion) {
	if target == nil || fallback == nil {
		return
	}
	if fallback.HasReviewVerdict && !target.HasReviewVerdict {
		target.HasReviewVerdict = true
		target.ReviewReady = fallback.ReviewReady
	}
	if len(fallback.Artifacts) > 0 {
		if target.Artifacts == nil {
			target.Artifacts = map[string]string{}
		}
		for key, value := range fallback.Artifacts {
			if strings.TrimSpace(target.Artifacts[key]) == "" && strings.TrimSpace(value) != "" {
				target.Artifacts[key] = value
			}
		}
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

func marshalCompletionJSON(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	data, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	return string(data)
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

func lookupRawString(data map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			switch typed := value.(type) {
			case string:
				if typed != "" {
					return typed, true
				}
			case fmt.Stringer:
				text := typed.String()
				if text != "" {
					return text, true
				}
			}
		}
	}
	return "", false
}

func lookupAny(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if data == nil {
			return nil
		}
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func hasRawString(data map[string]any, keys ...string) bool {
	_, ok := lookupRawString(data, keys...)
	return ok
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

func commandApprovalParts(params map[string]any) []string {
	if command := lookupString(params, "command"); command != "" {
		return strings.Fields(command)
	}
	return stringSlice(lookupSlice(params, "command", "args", "argv"))
}

func approvalRequestID(message contracts.JSONRPCMessage, params map[string]any) string {
	if approvalID := lookupString(params, "approvalId", "approval_id", "id"); approvalID != "" {
		return approvalID
	}
	return jsonRPCIDString(message.ID)
}

func jsonRPCIDString(id json.RawMessage) string {
	raw := strings.TrimSpace(string(id))
	if raw == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(id, &decoded); err != nil {
		return raw
	}
	return anyString(decoded)
}

func anyString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return fmt.Sprintf("%g", typed)
	case float32:
		return fmt.Sprintf("%g", typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case int32:
		return fmt.Sprintf("%d", typed)
	case uint:
		return fmt.Sprintf("%d", typed)
	case uint64:
		return fmt.Sprintf("%d", typed)
	case uint32:
		return fmt.Sprintf("%d", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func normalizeCodexTokenUsage(params map[string]any) map[string]string {
	usage := lookupMap(params, "tokenUsage", "token_usage", "usage")
	total := lookupMap(usage, "total")
	return map[string]string{
		"input_tokens": firstTokenValue(
			lookupAny(params, "inputTokens", "input_tokens"),
			lookupAny(total, "inputTokens", "input_tokens"),
			lookupAny(usage, "inputTokens", "input_tokens"),
		),
		"output_tokens": firstTokenValue(
			lookupAny(params, "outputTokens", "output_tokens"),
			lookupAny(total, "outputTokens", "output_tokens"),
			lookupAny(usage, "outputTokens", "output_tokens"),
		),
		"total_tokens": firstTokenValue(
			lookupAny(params, "totalTokens", "total_tokens"),
			lookupAny(total, "totalTokens", "total_tokens"),
			lookupAny(usage, "totalTokens", "total_tokens"),
		),
	}
}

func firstTokenValue(values ...any) string {
	for _, value := range values {
		if text := anyString(value); text != "" {
			return text
		}
	}
	return ""
}

func commandString(item map[string]any) string {
	if command := lookupString(item, "command"); command != "" {
		return command
	}
	parts := stringSlice(lookupSlice(item, "command"))
	if len(parts) == 0 {
		parts = stringSlice(lookupSlice(item, "args", "argv"))
	}
	return strings.Join(parts, " ")
}

func commandOutcome(item map[string]any) string {
	exitCode := strings.TrimSpace(anyString(lookupAny(item, "exitCode", "exit_code")))
	if exitCode == "" {
		return ""
	}
	if exitCode == "0" {
		return "ok"
	}
	return "error"
}

func isCommandExecutionItem(itemType string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(itemType), "_", ""))
	return normalized == "commandexecution"
}

func isToolItem(itemType string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(itemType), "_", ""))
	switch normalized {
	case "", "commandexecution", "agentmessage":
		return false
	default:
		return true
	}
}

func isAutoApprovalReviewDenied(method string, itemType string, params map[string]any, item map[string]any) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(itemType), "_", ""))
	if normalized != "autoapprovalreview" && method != "item/autoApprovalReview/completed" {
		return false
	}
	review := autoApprovalReview(params, item)
	status := strings.ToLower(coalesceMessage(
		lookupString(review, "status", "state", "outcome"),
		lookupString(params, "action", "status", "state", "outcome"),
		lookupString(item, "status", "state", "outcome"),
	))
	return status == "denied" || status == "deny" || status == "rejected" || status == "blocked"
}

func autoApprovalReview(params map[string]any, item map[string]any) map[string]any {
	if review := lookupMap(params, "review"); review != nil {
		return review
	}
	return lookupMap(item, "review")
}

func guardianApprovalReviewAction(params map[string]any, item map[string]any) map[string]any {
	if action := lookupMap(params, "action"); action != nil {
		return action
	}
	return lookupMap(item, "action")
}

func legacyGuardianActionValue(params map[string]any) string {
	if action := lookupString(params, "action"); action != "" {
		return action
	}
	return ""
}

func guardianActionType(params map[string]any, action map[string]any) string {
	if actionType := lookupString(action, "type"); actionType != "" {
		return actionType
	}
	return legacyGuardianActionValue(params)
}

func guardianActionCommand(action map[string]any) string {
	switch lookupString(action, "type") {
	case "command":
		return lookupString(action, "command")
	case "execve":
		if argv := stringSlice(lookupSlice(action, "argv")); len(argv) > 0 {
			return strings.Join(argv, " ")
		}
		return lookupString(action, "program")
	default:
		return ""
	}
}

func toolTarget(item map[string]any) string {
	for _, key := range []string{"path", "filePath", "file_path", "target"} {
		if target := lookupString(item, key); target != "" {
			return target
		}
	}
	for _, change := range lookupSlice(item, "changes") {
		mapped, ok := change.(map[string]any)
		if !ok {
			continue
		}
		if target := lookupString(mapped, "path", "filePath", "file_path", "target"); target != "" {
			return target
		}
	}
	return ""
}

func blockReasonFromCodexError(errorInfo map[string]any) contracts.BlockReason {
	codexErrorInfo := lookupAny(errorInfo, "codexErrorInfo", "codex_error_info")
	errorText := strings.ToLower(strings.Join(filterEmpty([]string{
		lookupString(errorInfo, "type", "code", "message"),
		extractText(errorInfo),
		codexErrorInfoName(codexErrorInfo),
		extractText(codexErrorInfo),
	}), " "))
	status := codexErrorInfoHTTPStatus(codexErrorInfo)
	switch {
	case status == "429" || strings.Contains(errorText, "rate") || strings.Contains(errorText, "limit"):
		return contracts.BlockReasonRateLimited
	case status == "401" || status == "403" || strings.Contains(errorText, "auth") || strings.Contains(errorText, "unauthorized"):
		return contracts.BlockReasonAuth
	case strings.Contains(errorText, "permission") || strings.Contains(errorText, "denied"):
		return contracts.BlockReasonPermissionDenied
	default:
		return contracts.BlockReasonOther
	}
}

func codexErrorInfoName(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case map[string]any:
		if name := lookupString(typed, "type", "kind", "code"); name != "" {
			return name
		}
		for key := range typed {
			if strings.TrimSpace(key) != "" {
				return key
			}
		}
	}
	return ""
}

func codexErrorInfoHTTPStatus(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if status := anyString(lookupAny(typed, "httpStatusCode", "http_status_code", "status")); status != "" {
			return status
		}
		for _, nested := range typed {
			if mapped, ok := nested.(map[string]any); ok {
				if status := anyString(lookupAny(mapped, "httpStatusCode", "http_status_code", "status")); status != "" {
					return status
				}
			}
		}
	}
	return ""
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
			lookupString(typed, "delta", "text", "message", "title", "reason"),
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
