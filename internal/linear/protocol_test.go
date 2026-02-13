package linear

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeAgentSessionEventCreatedFixture(t *testing.T) {
	payload := readFixture(t, "agent_session_event.created.v1.json")
	event, err := DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode created fixture: %v", err)
	}

	if event.Action != AgentSessionEventActionCreated {
		t.Fatalf("expected action %q, got %q", AgentSessionEventActionCreated, event.Action)
	}
	if event.PayloadVersion != AgentSessionEventPayloadVersion1 {
		t.Fatalf("expected payloadVersion=%d, got %d", AgentSessionEventPayloadVersion1, event.PayloadVersion)
	}
	if event.AgentSession.ID == "" {
		t.Fatalf("expected non-empty agentSession.id")
	}
	if event.AgentSession.PromptContext == "" {
		t.Fatalf("expected non-empty agentSession.promptContext")
	}
	if event.AgentSession.State != AgentSessionStatePending {
		t.Fatalf("expected session state %q, got %q", AgentSessionStatePending, event.AgentSession.State)
	}
}

func TestDecodeAgentSessionEventPromptedFixture(t *testing.T) {
	payload := readFixture(t, "agent_session_event.prompted.v1.json")
	event, err := DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode prompted fixture: %v", err)
	}

	if event.Action != AgentSessionEventActionPrompted {
		t.Fatalf("expected action %q, got %q", AgentSessionEventActionPrompted, event.Action)
	}
	if event.AgentActivity == nil {
		t.Fatalf("expected prompted event to include agentActivity")
	}
	if event.AgentActivity.Content.Type != AgentActivityContentTypePrompt {
		t.Fatalf("expected prompted activity content type %q, got %q", AgentActivityContentTypePrompt, event.AgentActivity.Content.Type)
	}
	if event.AgentActivity.Content.Body == "" {
		t.Fatalf("expected prompted activity body")
	}
}

func TestDecodeAgentSessionEventRejectsUnsupportedPayloadVersion(t *testing.T) {
	payload := readFixture(t, "agent_session_event.created.v1.json")
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	doc["payloadVersion"] = 999
	invalidPayload, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	_, err = DecodeAgentSessionEvent(invalidPayload)
	if !errors.Is(err, ErrUnsupportedAgentSessionEventPayloadVersion) {
		t.Fatalf("expected unsupported payload version error, got %v", err)
	}
}

func TestDecodeAgentSessionEventRejectsPromptedWithoutPromptActivity(t *testing.T) {
	payload := readFixture(t, "agent_session_event.prompted.v1.json")
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	agentActivity, ok := doc["agentActivity"].(map[string]any)
	if !ok {
		t.Fatalf("fixture must include object agentActivity")
	}
	content, ok := agentActivity["content"].(map[string]any)
	if !ok {
		t.Fatalf("fixture must include object agentActivity.content")
	}
	content["type"] = "thought"

	invalidPayload, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	_, err = DecodeAgentSessionEvent(invalidPayload)
	if !errors.Is(err, ErrPromptedEventRequiresPromptActivity) {
		t.Fatalf("expected prompted validation error, got %v", err)
	}
}

func TestValidateAgentActivityContent(t *testing.T) {
	empty := ""
	validResult := "12°C and clear"

	cases := []struct {
		name    string
		content AgentActivityContent
		wantErr error
	}{
		{
			name: "thought requires body",
			content: AgentActivityContent{
				Type: AgentActivityContentTypeThought,
				Body: empty,
			},
			wantErr: ErrActivityBodyRequired,
		},
		{
			name: "action requires action label",
			content: AgentActivityContent{
				Type:      AgentActivityContentTypeAction,
				Parameter: "weather in SF",
			},
			wantErr: ErrActionLabelRequired,
		},
		{
			name: "action requires parameter",
			content: AgentActivityContent{
				Type:   AgentActivityContentTypeAction,
				Action: "searching",
			},
			wantErr: ErrActionParameterRequired,
		},
		{
			name: "response accepts body",
			content: AgentActivityContent{
				Type: AgentActivityContentTypeResponse,
				Body: "Final result",
			},
			wantErr: nil,
		},
		{
			name: "action accepts optional result",
			content: AgentActivityContent{
				Type:      AgentActivityContentTypeAction,
				Action:    "searched",
				Parameter: "weather in SF",
				Result:    &validResult,
			},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAgentActivityContent(tc.content)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestSessionStateForActivityType(t *testing.T) {
	cases := []struct {
		contentType AgentActivityContentType
		wantState   AgentSessionState
	}{
		{contentType: AgentActivityContentTypeThought, wantState: AgentSessionStateActive},
		{contentType: AgentActivityContentTypeAction, wantState: AgentSessionStateActive},
		{contentType: AgentActivityContentTypeElicitation, wantState: AgentSessionStateAwaitingInput},
		{contentType: AgentActivityContentTypeResponse, wantState: AgentSessionStateComplete},
		{contentType: AgentActivityContentTypeError, wantState: AgentSessionStateError},
		{contentType: AgentActivityContentTypePrompt, wantState: AgentSessionStateActive},
	}

	for _, tc := range cases {
		got := SessionStateForActivityType(tc.contentType)
		if got != tc.wantState {
			t.Fatalf("activity %q expected state %q, got %q", tc.contentType, tc.wantState, got)
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}
