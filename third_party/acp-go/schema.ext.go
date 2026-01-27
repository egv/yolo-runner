package acp

import "encoding/json"

// Helper functions for constructing discriminated unions
// These functions are provided to make it easier to create the various types
// without having to understand the internal structure of discriminated unions.

// SessionUpdate constructors

// NewSessionUpdateAgentMessageChunk creates a SessionUpdate with agent message chunk
func NewSessionUpdateAgentMessageChunk(content ContentBlock) SessionUpdate {
	return SessionUpdate{
		discriminator: "agent_message_chunk",
		agentmessagechunk: &SessionUpdateAgentmessagechunk{
			Content:       content,
			SessionUpdate: "agent_message_chunk",
		},
	}
}

// NewSessionUpdateUserMessageChunk creates a SessionUpdate with user message chunk
func NewSessionUpdateUserMessageChunk(content ContentBlock) SessionUpdate {
	return SessionUpdate{
		discriminator: "user_message_chunk",
		usermessagechunk: &SessionUpdateUsermessagechunk{
			Content:       content,
			SessionUpdate: "user_message_chunk",
		},
	}
}

// NewSessionUpdateAgentThoughtChunk creates a SessionUpdate with agent thought chunk
func NewSessionUpdateAgentThoughtChunk(content ContentBlock) SessionUpdate {
	return SessionUpdate{
		discriminator: "agent_thought_chunk",
		agentthoughtchunk: &SessionUpdateAgentthoughtchunk{
			Content:       content,
			SessionUpdate: "agent_thought_chunk",
		},
	}
}

// NewSessionUpdateToolCall creates a SessionUpdate with tool call
func NewSessionUpdateToolCall(toolCallId ToolCallId, title string, kind *ToolKind, status *ToolCallStatus, locations []ToolCallLocation, rawInput interface{}) SessionUpdate {
	var rawInputJSON json.RawMessage
	if rawInput != nil {
		if bytes, err := json.Marshal(rawInput); err == nil {
			rawInputJSON = bytes
		}
	}

	return SessionUpdate{
		discriminator: "tool_call",
		toolcall: &SessionUpdateToolcall{
			ToolCallId:    toolCallId,
			Title:         title,
			Kind:          kind,
			Status:        status,
			Locations:     locations,
			RawInput:      rawInputJSON,
			SessionUpdate: "tool_call",
		},
	}
}

// NewSessionUpdateToolCallUpdate creates a SessionUpdate with tool call update
func NewSessionUpdateToolCallUpdate(toolCallId ToolCallId, status *ToolCallStatus, content []ToolCallContent, rawOutput interface{}) SessionUpdate {
	var rawOutputJSON json.RawMessage
	if rawOutput != nil {
		if bytes, err := json.Marshal(rawOutput); err == nil {
			rawOutputJSON = bytes
		}
	}

	return SessionUpdate{
		discriminator: "tool_call_update",
		toolcallupdate: &SessionUpdateToolcallupdate{
			ToolCallId:    toolCallId,
			Status:        status,
			Content:       content,
			RawOutput:     rawOutputJSON,
			SessionUpdate: "tool_call_update",
		},
	}
}

// NewSessionUpdatePlan creates a SessionUpdate with plan
func NewSessionUpdatePlan(entries []PlanEntry) SessionUpdate {
	return SessionUpdate{
		discriminator: "plan",
		plan: &SessionUpdatePlan{
			Entries:       entries,
			SessionUpdate: "plan",
		},
	}
}

// ContentBlock constructors

// NewContentBlockText creates a text content block
func NewContentBlockText(text string) ContentBlock {
	return ContentBlock{
		discriminator: "text",
		text: &ContentBlockText{
			Type: "text",
			Text: text,
		},
	}
}

// NewContentBlockImage creates an image content block
func NewContentBlockImage(data, mimeType string, uri string) ContentBlock {
	return ContentBlock{
		discriminator: "image",
		image: &ContentBlockImage{
			Type:     "image",
			Data:     data,
			MimeType: mimeType,
			Uri:      uri,
		},
	}
}

// NewContentBlockResourceLink creates a resource link content block
func NewContentBlockResourceLink(name, uri string, title, description, mimeType string, size *int64) ContentBlock {
	return ContentBlock{
		discriminator: "resource_link",
		resourcelink: &ContentBlockResourcelink{
			Type:        "resource_link",
			Name:        name,
			Uri:         uri,
			Title:       title,
			Description: description,
			Size:        size,
			MimeType:    mimeType,
		},
	}
}

// RequestPermissionOutcome constructors

// NewRequestPermissionOutcomeSelected creates a selected permission outcome
func NewRequestPermissionOutcomeSelected(optionId PermissionOptionId) RequestPermissionOutcome {
	return RequestPermissionOutcome{
		discriminator: "selected",
		selected: &RequestPermissionOutcomeSelected{
			Outcome:  "selected",
			OptionId: optionId,
		},
	}
}

// NewRequestPermissionOutcomeCancelled creates a cancelled permission outcome
func NewRequestPermissionOutcomeCancelled() RequestPermissionOutcome {
	return RequestPermissionOutcome{
		discriminator: "cancelled",
		cancelled: &RequestPermissionOutcomeCancelled{
			Outcome: "cancelled",
		},
	}
}

// ToolCallContent constructors

// NewToolCallContentContent creates tool call content with a content block
func NewToolCallContentContent(content ContentBlock) ToolCallContent {
	return ToolCallContent{
		discriminator: "content",
		content: &ToolCallContentContent{
			Type:    "content",
			Content: content,
		},
	}
}

// NewToolCallContentDiff creates tool call content with a diff
func NewToolCallContentDiff(path, newText, oldText string) ToolCallContent {
	return ToolCallContent{
		discriminator: "diff",
		diff: &ToolCallContentDiff{
			Type:    "diff",
			Path:    path,
			NewText: newText,
			OldText: oldText,
		},
	}
}

// Helper functions for pointers

// StringPtr returns a pointer to a string
func StringPtr(s string) *string {
	return &s
}

// Int64Ptr returns a pointer to an int64
func Int64Ptr(i int64) *int64 {
	return &i
}

// ToolKindPtr returns a pointer to a ToolKind
func ToolKindPtr(k ToolKind) *ToolKind {
	return &k
}

// ToolCallStatusPtr returns a pointer to a ToolCallStatus
func ToolCallStatusPtr(s ToolCallStatus) *ToolCallStatus {
	return &s
}
