package opencode

import (
	"strings"

	acp "github.com/ironpark/acp-go"
)

// RunnerEvent is the minimal shape needed to render runner-side events in the
// OpenCode TUI log bubble store.
//
// It is intentionally an interface to avoid an import cycle with internal/runner.
type RunnerEvent interface {
	RunnerEventType() string
	RunnerEventTitle() string
	RunnerEventThought() string
	RunnerEventMessage() string
}

// EventRouter routes ACP updates (from opencode) and runner events (from yolo-runner)
// into a shared LogBubbleStore.
type EventRouter struct {
	store *LogBubbleStore
}

func NewEventRouter(store *LogBubbleStore) *EventRouter {
	return &EventRouter{store: store}
}

func (er *EventRouter) RouteACPUpdate(update *acp.SessionUpdate) error {
	if er == nil || er.store == nil {
		return nil
	}
	if update == nil {
		return nil
	}

	if toolCall := update.GetToolcall(); toolCall != nil {
		er.store.UpsertToolCall(&acp.ToolCall{
			ToolCallId: toolCall.ToolCallId,
			Title:      toolCall.Title,
			Kind:       toolCall.Kind,
			Status:     toolCall.Status,
			Content:    toolCall.Content,
			Locations:  toolCall.Locations,
			RawInput:   toolCall.RawInput,
			RawOutput:  toolCall.RawOutput,
		})
		return nil
	}
	if toolUpdate := update.GetToolcallupdate(); toolUpdate != nil {
		er.store.UpsertToolCallUpdate(&acp.ToolCallUpdate{
			ToolCallId: toolUpdate.ToolCallId,
			Title:      toolUpdate.Title,
			Kind:       toolUpdate.Kind,
			Status:     toolUpdate.Status,
			Content:    toolUpdate.Content,
			Locations:  toolUpdate.Locations,
			RawInput:   toolUpdate.RawInput,
			RawOutput:  toolUpdate.RawOutput,
		})
		return nil
	}
	// Only surface textual updates in the log view; skip plan/mode/commands noise.
	if update.GetAgentmessagechunk() == nil && update.GetUsermessagechunk() == nil && update.GetAgentthoughtchunk() == nil {
		return nil
	}
	if line := formatSessionUpdate(update); line != "" {
		er.store.AddLogEntry(line)
	}
	return nil
}

func (er *EventRouter) RouteRunnerEvent(event RunnerEvent) error {
	if er == nil || er.store == nil {
		return nil
	}
	if event == nil {
		return nil
	}
	if event.RunnerEventType() == "" {
		return nil
	}

	parts := []string{event.RunnerEventType()}
	if title := event.RunnerEventTitle(); title != "" {
		parts = append(parts, title)
	}
	if thought := event.RunnerEventThought(); thought != "" {
		parts = append(parts, thought)
	}
	if message := event.RunnerEventMessage(); message != "" {
		parts = append(parts, message)
	}
	entry := strings.Join(parts, " ")

	switch event.RunnerEventType() {
	case "runner_cmd_started":
		er.store.AppendRunnerCmdEntry(entry)
	case "runner_cmd_finished":
		er.store.MutateLastRunnerCmdEntry(entry)
	default:
		er.store.AddLogEntry(entry)
	}
	return nil
}
