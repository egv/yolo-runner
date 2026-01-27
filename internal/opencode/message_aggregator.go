package opencode

import (
	"strings"

	acp "github.com/ironpark/acp-go"
)

type AgentMessageAggregator struct {
	agentMessageBuffer  strings.Builder
	userMessageBuffer   strings.Builder
	agentThoughtBuffer  strings.Builder
}

func NewAgentMessageAggregator() *AgentMessageAggregator {
	return &AgentMessageAggregator{}
}

func (a *AgentMessageAggregator) ProcessUpdate(update *acp.SessionUpdate) string {
	if update == nil {
		return ""
	}

	if toolCall := update.GetToolcall(); toolCall != nil {
		return formatToolCall("tool_call", toolCall.ToolCallId, toolCall.Title, toolCall.Kind, toolCall.Status)
	}
	if toolUpdate := update.GetToolcallupdate(); toolUpdate != nil {
		return formatToolCall("tool_call_update", toolUpdate.ToolCallId, toolUpdate.Title, toolUpdate.Kind, toolUpdate.Status)
	}
	if message := update.GetAgentmessagechunk(); message != nil {
		return a.processMessageChunk(&message.Content)
	}
	if message := update.GetUsermessagechunk(); message != nil {
		return a.processUserMessageChunk(&message.Content)
	}
	if thought := update.GetAgentthoughtchunk(); thought != nil {
		return a.processAgentThoughtChunk(&thought.Content)
	}
	if plan := update.GetPlan(); plan != nil {
		return ""
	}
	if commands := update.GetAvailablecommandsupdate(); commands != nil {
		return ""
	}
	if mode := update.GetCurrentmodeupdate(); mode != nil {
		return ""
	}
	return ""
}

func (a *AgentMessageAggregator) processMessageChunk(content *acp.ContentBlock) string {
	if content == nil {
		return ""
	}

	text := ""
	if content.IsText() {
		text = content.GetText().Text
	}
	if text == "" {
		return ""
	}

	a.agentMessageBuffer.WriteString(text)

	// Check if the accumulated content contains a newline
	accumulated := a.agentMessageBuffer.String()
	if strings.Contains(accumulated, "\n") {
		// Find the last newline and output everything up to and including it
		lastNewlineIndex := strings.LastIndex(accumulated, "\n")
		output := accumulated[:lastNewlineIndex+1]

		// Keep any remaining content after the last newline in the buffer
		remaining := accumulated[lastNewlineIndex+1:]
		a.agentMessageBuffer.Reset()
		a.agentMessageBuffer.WriteString(remaining)

		contentBlock := acp.NewContentBlockText(output)
		return formatMessage("agent_message", &contentBlock)
	}

	// No newline yet, don't output anything
	return ""
}

func (a *AgentMessageAggregator) processUserMessageChunk(content *acp.ContentBlock) string {
	if content == nil {
		return ""
	}

	text := ""
	if content.IsText() {
		text = content.GetText().Text
	}
	if text == "" {
		return ""
	}

	a.userMessageBuffer.WriteString(text)

	// Check if the accumulated content contains a newline
	accumulated := a.userMessageBuffer.String()
	if strings.Contains(accumulated, "\n") {
		// Find the last newline and output everything up to and including it
		lastNewlineIndex := strings.LastIndex(accumulated, "\n")
		output := accumulated[:lastNewlineIndex+1]

		// Keep any remaining content after the last newline in the buffer
		remaining := accumulated[lastNewlineIndex+1:]
		a.userMessageBuffer.Reset()
		a.userMessageBuffer.WriteString(remaining)

		contentBlock := acp.NewContentBlockText(output)
		return formatMessage("user_message", &contentBlock)
	}

	// No newline yet, don't output anything
	return ""
}

func (a *AgentMessageAggregator) processAgentThoughtChunk(content *acp.ContentBlock) string {
	if content == nil {
		return ""
	}

	text := ""
	if content.IsText() {
		text = content.GetText().Text
	}
	if text == "" {
		return ""
	}

	a.agentThoughtBuffer.WriteString(text)

	// Check if the accumulated content contains a newline
	accumulated := a.agentThoughtBuffer.String()
	if strings.Contains(accumulated, "\n") {
		// Find the last newline and output everything up to and including it
		lastNewlineIndex := strings.LastIndex(accumulated, "\n")
		output := accumulated[:lastNewlineIndex+1]

		// Keep any remaining content after the last newline in the buffer
		remaining := accumulated[lastNewlineIndex+1:]
		a.agentThoughtBuffer.Reset()
		a.agentThoughtBuffer.WriteString(remaining)

		// Normalize the text by replacing newlines and carriage returns with spaces
		normalizedOutput := normalizeAgentThoughtText(output)
		contentBlock := acp.NewContentBlockText(normalizedOutput)
		return formatMessage("agent_thought", &contentBlock)
	}

	// No newline yet, don't output anything
	return ""
}
