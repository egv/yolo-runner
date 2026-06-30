package opencode

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	acp "github.com/ironpark/acp-go"
)

// NormalizeNativeACPJSONL replays an opencode ACP transcript into canonical
// runner progress.
func NormalizeNativeACPJSONL(reader io.Reader) ([]contracts.RunnerProgress, error) {
	progress := []contracts.RunnerProgress{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event opencodeNativeFixtureEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, err
		}
		events := normalizeOpenCodeNativeFixtureEvent(event)
		for _, p := range events {
			markOpenCodeParityMetadata(&p)
			progress = append(progress, p)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return progress, nil
}

type opencodeNativeFixtureEvent struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id"`
	Update     string `json:"update"`
	Text       string `json:"text"`
	ToolCallID string `json:"tool_call_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Target     string `json:"target"`
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"`
	StopReason string `json:"stop_reason"`
}

func normalizeOpenCodeNativeFixtureEvent(event opencodeNativeFixtureEvent) []contracts.RunnerProgress {
	switch event.Type {
	case "session_notification":
		if event.Update == "agent_message" {
			p, ok := NormalizeACPProgressNotification(&acp.SessionNotification{
				SessionId: acp.SessionId(event.SessionID),
				Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(event.Text)),
			})
			if ok {
				return []contracts.RunnerProgress{p}
			}
		}
		if event.Update == "tool_call" {
			kind := acp.ToolKind(event.Kind)
			status := acp.ToolCallStatus(event.Status)
			p, ok := NormalizeACPProgressNotification(&acp.SessionNotification{
				SessionId: acp.SessionId(event.SessionID),
				Update: acp.NewSessionUpdateToolCall(
					acp.ToolCallId(event.ToolCallID),
					event.Title,
					&kind,
					&status,
					nil,
					nil,
				),
			})
			if ok {
				if p.Metadata == nil {
					p.Metadata = map[string]string{}
				}
				if strings.TrimSpace(event.Target) != "" {
					p.Metadata["target"] = event.Target
					p.Metadata["path"] = event.Target
				}
				return []contracts.RunnerProgress{p}
			}
		}
	case "approval_required":
		decision := contracts.TaskSessionApprovalOutcome(event.Decision)
		p, ok := NormalizeOpencodeTaskSessionEvent(contracts.TaskSessionEvent{
			Type:      contracts.TaskSessionEventTypeApprovalRequired,
			SessionID: event.SessionID,
			Approval: &contracts.TaskSessionApprovalEvent{
				Request: contracts.TaskSessionApprovalRequest{
					ID:    event.ApprovalID,
					Kind:  contracts.TaskSessionApprovalKindToolCall,
					Title: event.Title,
				},
				Decision: &contracts.TaskSessionApprovalDecision{Outcome: decision},
			},
		})
		if ok {
			return []contracts.RunnerProgress{p}
		}
	case "prompt_response":
		p, ok := NormalizeACPPromptResponse(&acp.PromptResponse{StopReason: acp.StopReason(event.StopReason)})
		if ok {
			return []contracts.RunnerProgress{p}
		}
	}
	return nil
}

func markOpenCodeParityMetadata(progress *contracts.RunnerProgress) {
	if progress == nil {
		return
	}
	if progress.Type == string(contracts.EventTypeAgentText) && strings.Contains(progress.Message, "Exploring parity fixture") {
		if progress.Metadata == nil {
			progress.Metadata = map[string]string{}
		}
		progress.Metadata["parity_step"] = "explore"
	}
}
