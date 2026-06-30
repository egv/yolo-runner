package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// NormalizeNativeAppServerJSONL replays codex app-server JSON-RPC notifications
// into canonical runner progress.
func NormalizeNativeAppServerJSONL(reader io.Reader, mode contracts.RunnerMode) ([]contracts.RunnerProgress, error) {
	progress := []contracts.RunnerProgress{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message contracts.JSONRPCMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			return nil, err
		}
		p, _, ok := RunnerProgressFromAppServerNotification(message, mode)
		if !ok {
			continue
		}
		if message.Method == "turn/completed" {
			p.Type = string(contracts.EventTypeAgentFinished)
			p.Message = "finished"
			p.Metadata = map[string]string{"stop_reason": strings.TrimSpace(p.Metadata["reason"])}
			if p.Metadata["stop_reason"] == "" {
				p.Metadata["stop_reason"] = "completed"
			}
			p.Timestamp = time.Now().UTC()
		}
		progress = append(progress, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return progress, nil
}
