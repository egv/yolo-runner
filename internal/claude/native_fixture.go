package claude

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// NormalizeNativeStreamJSONL replays a Claude stream-json transcript into the
// same canonical progress events emitted by the runner adapter.
func NormalizeNativeStreamJSONL(reader io.Reader) ([]contracts.RunnerProgress, error) {
	progress := []contracts.RunnerProgress{}
	emit := func(p contracts.RunnerProgress) {
		if p.Timestamp.IsZero() {
			p.Timestamp = time.Now().UTC()
		}
		markClaudeParityMetadata(&p)
		progress = append(progress, p)
	}
	emitter := newClaudeStreamProgressEmitter(emit, time.Now)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		emitClaudeNativeTextAndLifecycle(line, emit)
		emitClaudePermissionDenied(line, emit)
		emitter.HandleLine(line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return progress, nil
}

func emitClaudeNativeTextAndLifecycle(line string, emit func(contracts.RunnerProgress)) {
	var event claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Type == "result" {
		emit(contracts.RunnerProgress{
			Type:     string(contracts.EventTypeAgentFinished),
			Message:  "finished",
			Metadata: map[string]string{"subtype": "success"},
		})
		return
	}
	if event.Type != "assistant" {
		return
	}
	for _, content := range event.Message.Content {
		if content.Type != "text" || strings.TrimSpace(content.Text) == "" {
			continue
		}
		emit(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeAgentText),
			Message: content.Text,
		})
	}
}

func emitClaudePermissionDenied(line string, emit func(contracts.RunnerProgress)) {
	var event claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	for _, content := range event.Message.Content {
		if content.Type != "tool_result" || !content.IsError {
			continue
		}
		detail := resultText(claudeStreamContent{Text: content.Text, Content: content.Content})
		if !claudeLooksPermissionDenied(detail) {
			continue
		}
		emit(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeAgentBlocked),
			Message: detail,
			Metadata: map[string]string{
				"approval_id": content.ToolUseID,
				"reason":      string(contracts.BlockReasonPermissionDenied),
				"detail":      detail,
			},
		})
	}
}

func claudeLooksPermissionDenied(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(normalized, "permission") && (strings.Contains(normalized, "denied") || strings.Contains(normalized, "haven't granted"))
}

func markClaudeParityMetadata(progress *contracts.RunnerProgress) {
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
