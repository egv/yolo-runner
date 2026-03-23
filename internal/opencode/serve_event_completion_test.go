package opencode

import (
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// TestDetectServeEventCompletionReturnsFalseForNonTerminalEvents verifies that
// intermediate events do not trigger completion detection.
func TestDetectServeEventCompletionReturnsFalseForNonTerminalEvents(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{
			name: "message part added",
			data: `{"type":"message.part.added","properties":{"part":{"type":"text","text":"Hello"}}}`,
		},
		{
			name: "message part updated",
			data: `{"type":"message.part.updated","properties":{"part":{"type":"text","text":"World"}}}`,
		},
		{
			name: "empty data",
			data: `{}`,
		},
		{
			name: "missing type",
			data: `{"properties":{}}`,
		},
		{
			name: "session updated without terminal status",
			data: `{"type":"session.updated","properties":{"session":{"status":"running"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := contracts.SSEEvent{Event: "event", Data: tc.data}
			completion, ok := DetectServeEventCompletion(event)
			if ok {
				t.Fatalf("expected non-terminal event for %q, got completion %#v", tc.name, completion)
			}
			if completion != nil {
				t.Fatalf("expected nil completion for non-terminal event %q, got %#v", tc.name, completion)
			}
		})
	}
}

// TestDetectServeEventCompletionDetectsSessionIdle verifies that a session.idle event
// is recognized as a completed terminal state.
func TestDetectServeEventCompletionDetectsSessionIdle(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"session.idle","properties":{}}`,
	}

	completion, ok := DetectServeEventCompletion(event)
	if !ok {
		t.Fatal("expected session.idle to be detected as terminal")
	}
	if completion == nil {
		t.Fatal("expected non-nil completion for session.idle")
	}
	if completion.Status != ServeSessionCompleted {
		t.Fatalf("expected status %q for session.idle, got %q", ServeSessionCompleted, completion.Status)
	}
}

// TestDetectServeEventCompletionDetectsSessionError verifies that a session.error event
// is recognized as a failed terminal state.
func TestDetectServeEventCompletionDetectsSessionError(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"session.error","properties":{"error":"something went wrong"}}`,
	}

	completion, ok := DetectServeEventCompletion(event)
	if !ok {
		t.Fatal("expected session.error to be detected as terminal")
	}
	if completion == nil {
		t.Fatal("expected non-nil completion for session.error")
	}
	if completion.Status != ServeSessionFailed {
		t.Fatalf("expected status %q for session.error, got %q", ServeSessionFailed, completion.Status)
	}
}

// TestDetectServeEventCompletionDetectsSessionCancelled verifies that a session.cancelled event
// is recognized as a stopped terminal state.
func TestDetectServeEventCompletionDetectsSessionCancelled(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"session.cancelled","properties":{}}`,
	}

	completion, ok := DetectServeEventCompletion(event)
	if !ok {
		t.Fatal("expected session.cancelled to be detected as terminal")
	}
	if completion == nil {
		t.Fatal("expected non-nil completion for session.cancelled")
	}
	if completion.Status != ServeSessionStopped {
		t.Fatalf("expected status %q for session.cancelled, got %q", ServeSessionStopped, completion.Status)
	}
}

// TestDetectServeEventCompletionReturnsFalseForInvalidJSON verifies that malformed data
// does not cause a panic and returns non-terminal.
func TestDetectServeEventCompletionReturnsFalseForInvalidJSON(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `not-valid-json`,
	}

	completion, ok := DetectServeEventCompletion(event)
	if ok {
		t.Fatalf("expected non-terminal for invalid JSON, got completion %#v", completion)
	}
	if completion != nil {
		t.Fatalf("expected nil completion for invalid JSON, got %#v", completion)
	}
}

// TestDetectServeEventCompletionReturnsFalseForBlankSSEEvent verifies that an empty
// SSE event is not treated as terminal.
func TestDetectServeEventCompletionReturnsFalseForBlankSSEEvent(t *testing.T) {
	event := contracts.SSEEvent{}

	completion, ok := DetectServeEventCompletion(event)
	if ok {
		t.Fatalf("expected non-terminal for blank SSE event, got completion %#v", completion)
	}
	if completion != nil {
		t.Fatalf("expected nil completion for blank SSE event, got %#v", completion)
	}
}

// TestDetectServeEventCompletionSessionIdleWithProperties verifies session.idle with
// full properties is still detected as completed.
func TestDetectServeEventCompletionSessionIdleWithProperties(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"session.idle","properties":{"session":{"id":"ses_abc","status":"idle"}}}`,
	}

	completion, ok := DetectServeEventCompletion(event)
	if !ok {
		t.Fatal("expected session.idle with properties to be terminal")
	}
	if completion.Status != ServeSessionCompleted {
		t.Fatalf("expected completed status, got %q", completion.Status)
	}
}

// TestDetectServeEventCompletionSessionErrorPreservesReason verifies that the error
// reason from the event properties is captured in the completion.
func TestDetectServeEventCompletionSessionErrorPreservesReason(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"session.error","properties":{"error":"context deadline exceeded"}}`,
	}

	completion, ok := DetectServeEventCompletion(event)
	if !ok {
		t.Fatal("expected session.error to be terminal")
	}
	if completion.Reason == "" {
		t.Fatal("expected reason to be populated from error properties")
	}
}
