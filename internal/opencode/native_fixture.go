package opencode

import (
	"bufio"
	"context"
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
		var record opencodeNativeACPRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, err
		}
		events, err := normalizeOpenCodeNativeFixtureEvent(record)
		if err != nil {
			return nil, err
		}
		for _, p := range events {
			progress = append(progress, p)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return progress, nil
}

type opencodeNativeACPRecord struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func normalizeOpenCodeNativeFixtureEvent(record opencodeNativeACPRecord) ([]contracts.RunnerProgress, error) {
	switch strings.TrimSpace(record.Method) {
	case "session/update":
		var notification acp.SessionNotification
		if err := json.Unmarshal(record.Params, &notification); err != nil {
			return nil, err
		}
		p, ok := NormalizeACPProgressNotification(&notification)
		if !ok {
			return nil, nil
		}
		return []contracts.RunnerProgress{p}, nil
	case "client/requestPermission":
		var request acp.RequestPermissionRequest
		if err := json.Unmarshal(record.Params, &request); err != nil {
			return nil, err
		}
		progress := []contracts.RunnerProgress{}
		client := &acpClient{
			handler:       NewACPHandler("parity", "runner-logs/opencode/parity.jsonl", nil),
			taskSessionID: string(request.SessionId),
		}
		client.setEventSink(contracts.TaskSessionEventSinkFunc(func(_ context.Context, event contracts.TaskSessionEvent) error {
			if p, ok := NormalizeOpencodeTaskSessionEvent(event); ok {
				progress = append(progress, p)
			}
			return nil
		}))
		if _, err := client.RequestPermission(context.Background(), &request); err != nil {
			return nil, err
		}
		return progress, nil
	case "session/promptResponse":
		var response acp.PromptResponse
		if err := json.Unmarshal(record.Params, &response); err != nil {
			return nil, err
		}
		p, ok := NormalizeACPPromptResponse(&response)
		if !ok {
			return nil, nil
		}
		return []contracts.RunnerProgress{p}, nil
	}
	return nil, nil
}
