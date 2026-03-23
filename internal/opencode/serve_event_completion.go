package opencode

import (
	"encoding/json"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// ServeSessionStatus describes how a serve task session ended.
type ServeSessionStatus string

const (
	ServeSessionCompleted ServeSessionStatus = "completed"
	ServeSessionFailed    ServeSessionStatus = "failed"
	ServeSessionStopped   ServeSessionStatus = "stopped"
)

// ServeEventCompletion holds the terminal state detected from an OpenCode serve SSE event.
type ServeEventCompletion struct {
	// Status is one of ServeSessionCompleted, ServeSessionFailed, or ServeSessionStopped.
	Status ServeSessionStatus
	// Reason is an optional human-readable reason extracted from event properties.
	Reason string
}

// DetectServeEventCompletion inspects a decoded SSE event from the OpenCode serve stream
// and determines whether it signals a terminal state (completed, failed, or stopped).
//
// Returns (completion, true) for terminal events, (nil, false) for non-terminal events.
func DetectServeEventCompletion(event contracts.SSEEvent) (*ServeEventCompletion, bool) {
	data := strings.TrimSpace(event.Data)
	if data == "" {
		return nil, false
	}

	var payload struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return nil, false
	}

	eventType := strings.TrimSpace(payload.Type)
	switch eventType {
	case "session.idle":
		return &ServeEventCompletion{Status: ServeSessionCompleted}, true
	case "session.error":
		reason := extractServeEventErrorReason(payload.Properties)
		return &ServeEventCompletion{Status: ServeSessionFailed, Reason: reason}, true
	case "session.cancelled":
		return &ServeEventCompletion{Status: ServeSessionStopped}, true
	}

	return nil, false
}

func extractServeEventErrorReason(properties map[string]any) string {
	if properties == nil {
		return ""
	}
	for _, key := range []string{"error", "message", "reason"} {
		if raw, ok := properties[key]; ok {
			if s, ok := raw.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	return ""
}
