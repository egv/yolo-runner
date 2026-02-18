package linear

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAgentActivityClientValidatesConfig(t *testing.T) {
	t.Run("missing endpoint", func(t *testing.T) {
		_, err := NewAgentActivityClient(AgentActivityClientConfig{
			Token: "token",
		})
		if err == nil {
			t.Fatalf("expected missing endpoint to fail")
		}
		if !strings.Contains(err.Error(), "endpoint") {
			t.Fatalf("expected endpoint validation error, got %q", err.Error())
		}
	})

	t.Run("missing token", func(t *testing.T) {
		_, err := NewAgentActivityClient(AgentActivityClientConfig{
			Endpoint: "http://linear.invalid/graphql",
		})
		if err == nil {
			t.Fatalf("expected missing token to fail")
		}
		if !strings.Contains(err.Error(), "token") {
			t.Fatalf("expected token validation error, got %q", err.Error())
		}
	})
}

func TestAgentActivityClientEmitThoughtMutation(t *testing.T) {
	captured := graphQLRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST method, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer linear-token" {
			t.Fatalf("expected bearer authorization, got %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("expected application/json content type, got %q", got)
		}
		decodeGraphQLRequest(t, r, &captured)
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentActivityCreate": map[string]any{
					"success": true,
					"agentActivity": map[string]any{
						"id": "activity-thought-1",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	gotID, err := client.EmitThought(context.Background(), ThoughtActivityInput{
		AgentSessionID: "session-1",
		Body:           "Thinking through the fix.",
		IdempotencyKey: "session-1:thought:1",
	})
	if err != nil {
		t.Fatalf("emit thought: %v", err)
	}
	if gotID != "activity-thought-1" {
		t.Fatalf("expected activity id %q, got %q", "activity-thought-1", gotID)
	}
	if !strings.Contains(captured.Query, "agentActivityCreate") {
		t.Fatalf("expected mutation query to call agentActivityCreate, got %q", captured.Query)
	}

	input := decodeMutationInput(t, captured)
	if got := stringFromMap(t, input, "agentSessionId"); got != "session-1" {
		t.Fatalf("expected agentSessionId session-1, got %q", got)
	}
	if got := stringFromMap(t, input, "id"); got == "" {
		t.Fatalf("expected idempotency id to be set")
	}
	content := mapFromMap(t, input, "content")
	if got := stringFromMap(t, content, "type"); got != string(AgentActivityContentTypeThought) {
		t.Fatalf("expected content type thought, got %q", got)
	}
	if got := stringFromMap(t, content, "body"); got != "Thinking through the fix." {
		t.Fatalf("expected thought body to round-trip, got %q", got)
	}
}

func TestAgentActivityClientEmitActionMutation(t *testing.T) {
	captured := graphQLRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeGraphQLRequest(t, r, &captured)
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentActivityCreate": map[string]any{
					"success": true,
					"agentActivity": map[string]any{
						"id": "activity-action-1",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	result := "2 files updated"
	gotID, err := client.EmitAction(context.Background(), ActionActivityInput{
		AgentSessionID: "session-1",
		Action:         "edit_file",
		Parameter:      "internal/linear/activity_client.go",
		Result:         &result,
		IdempotencyKey: "session-1:action:1",
	})
	if err != nil {
		t.Fatalf("emit action: %v", err)
	}
	if gotID != "activity-action-1" {
		t.Fatalf("expected activity id %q, got %q", "activity-action-1", gotID)
	}

	input := decodeMutationInput(t, captured)
	content := mapFromMap(t, input, "content")
	if got := stringFromMap(t, content, "type"); got != string(AgentActivityContentTypeAction) {
		t.Fatalf("expected content type action, got %q", got)
	}
	if got := stringFromMap(t, content, "action"); got != "edit_file" {
		t.Fatalf("expected action label edit_file, got %q", got)
	}
	if got := stringFromMap(t, content, "parameter"); got != "internal/linear/activity_client.go" {
		t.Fatalf("expected action parameter to round-trip, got %q", got)
	}
	if got := stringFromMap(t, content, "result"); got != result {
		t.Fatalf("expected action result to round-trip, got %q", got)
	}
}

func TestAgentActivityClientEmitResponseMutation(t *testing.T) {
	captured := graphQLRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeGraphQLRequest(t, r, &captured)
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentActivityCreate": map[string]any{
					"success": true,
					"agentActivity": map[string]any{
						"id": "activity-response-1",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	gotID, err := client.EmitResponse(context.Background(), ResponseActivityInput{
		AgentSessionID: "session-1",
		Body:           "Completed. Here is the summary.",
		IdempotencyKey: "session-1:response:1",
	})
	if err != nil {
		t.Fatalf("emit response: %v", err)
	}
	if gotID != "activity-response-1" {
		t.Fatalf("expected activity id %q, got %q", "activity-response-1", gotID)
	}

	input := decodeMutationInput(t, captured)
	content := mapFromMap(t, input, "content")
	if got := stringFromMap(t, content, "type"); got != string(AgentActivityContentTypeResponse) {
		t.Fatalf("expected content type response, got %q", got)
	}
	if got := stringFromMap(t, content, "body"); got != "Completed. Here is the summary." {
		t.Fatalf("expected response body to round-trip, got %q", got)
	}
}

func TestAgentActivityClientIdempotencyIDIsStablePerKey(t *testing.T) {
	var seenIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := graphQLRequest{}
		decodeGraphQLRequest(t, r, &req)
		input := decodeMutationInput(t, req)
		seenIDs = append(seenIDs, stringFromMap(t, input, "id"))
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentActivityCreate": map[string]any{
					"success": true,
					"agentActivity": map[string]any{
						"id": "activity",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.EmitThought(context.Background(), ThoughtActivityInput{
		AgentSessionID: "session-1",
		Body:           "step 1",
		IdempotencyKey: "session-1:thought:stable",
	})
	if err != nil {
		t.Fatalf("first emit thought: %v", err)
	}
	_, err = client.EmitThought(context.Background(), ThoughtActivityInput{
		AgentSessionID: "session-1",
		Body:           "step 1 retry",
		IdempotencyKey: "session-1:thought:stable",
	})
	if err != nil {
		t.Fatalf("second emit thought: %v", err)
	}
	_, err = client.EmitThought(context.Background(), ThoughtActivityInput{
		AgentSessionID: "session-1",
		Body:           "step 2",
		IdempotencyKey: "session-1:thought:different",
	})
	if err != nil {
		t.Fatalf("third emit thought: %v", err)
	}
	if len(seenIDs) != 3 {
		t.Fatalf("expected 3 seen ids, got %d", len(seenIDs))
	}
	if seenIDs[0] != seenIDs[1] {
		t.Fatalf("expected same id for same idempotency key, got %q and %q", seenIDs[0], seenIDs[1])
	}
	if seenIDs[0] == seenIDs[2] {
		t.Fatalf("expected different id for different idempotency key")
	}
}

func TestAgentActivityClientHandlesGraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeGraphQLResponse(t, w, map[string]any{
			"errors": []map[string]any{
				{
					"message": "rate limited",
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.EmitResponse(context.Background(), ResponseActivityInput{
		AgentSessionID: "session-1",
		Body:           "done",
		IdempotencyKey: "session-1:response:1",
	})
	if err == nil {
		t.Fatalf("expected graphql error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected graphql error details, got %q", err.Error())
	}
}

func TestAgentActivityClientHandlesUnsuccessfulMutation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentActivityCreate": map[string]any{
					"success":       false,
					"agentActivity": nil,
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.EmitResponse(context.Background(), ResponseActivityInput{
		AgentSessionID: "session-1",
		Body:           "done",
		IdempotencyKey: "session-1:response:1",
	})
	if err == nil {
		t.Fatalf("expected unsuccessful mutation error")
	}
	if !strings.Contains(err.Error(), "unsuccessful") {
		t.Fatalf("expected unsuccessful mutation error, got %q", err.Error())
	}
}

func TestAgentActivityClientValidatesInputs(t *testing.T) {
	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint: "http://linear.invalid/graphql",
		Token:    "linear-token",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.EmitThought(context.Background(), ThoughtActivityInput{
		Body:           "thinking",
		IdempotencyKey: "key-1",
	})
	if err == nil {
		t.Fatalf("expected missing session id validation failure")
	}

	_, err = client.EmitThought(context.Background(), ThoughtActivityInput{
		AgentSessionID: "session-1",
		Body:           "thinking",
	})
	if err == nil {
		t.Fatalf("expected missing idempotency key validation failure")
	}

	_, err = client.EmitAction(context.Background(), ActionActivityInput{
		AgentSessionID: "session-1",
		Parameter:      "path",
		IdempotencyKey: "key-2",
	})
	if err == nil {
		t.Fatalf("expected missing action label validation failure")
	}

	_, err = client.EmitAction(context.Background(), ActionActivityInput{
		AgentSessionID: "session-1",
		Action:         "edit_file",
		IdempotencyKey: "key-3",
	})
	if err == nil {
		t.Fatalf("expected missing action parameter validation failure")
	}

	_, err = client.EmitResponse(context.Background(), ResponseActivityInput{
		AgentSessionID: "session-1",
		IdempotencyKey: "key-4",
	})
	if err == nil {
		t.Fatalf("expected missing response body validation failure")
	}
}

func TestAgentActivityClientUpdateSessionExternalURLsMutationShape(t *testing.T) {
	captured := graphQLRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeGraphQLRequest(t, r, &captured)
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentSessionUpdate": map[string]any{
					"success": true,
					"agentSession": map[string]any{
						"id": "session-1",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.UpdateSessionExternalURLs(context.Background(), AgentSessionExternalURLsInput{
		AgentSessionID: "session-1",
		ExternalURLs: []ExternalURL{
			{Label: "Runner Session", URL: "https://example.test/sessions/session-1"},
			{Label: "Runner Log", URL: "file:///tmp/runner-logs/session-1.jsonl"},
		},
	})
	if err != nil {
		t.Fatalf("update session external urls: %v", err)
	}

	if got, want := strings.TrimSpace(captured.Query), strings.TrimSpace(updateAgentSessionMutation); got != want {
		t.Fatalf("expected exact mutation query shape\nwant: %q\n got: %q", want, got)
	}

	if len(captured.Variables) != 2 {
		t.Fatalf("expected exactly top-level id and input variables, got %#v", captured.Variables)
	}
	if got := stringFromMap(t, captured.Variables, "id"); got != "session-1" {
		t.Fatalf("expected top-level id variable session-1, got %q", got)
	}
	input := mapFromMap(t, captured.Variables, "input")
	if len(input) != 1 {
		t.Fatalf("expected input to contain only externalUrls, got %#v", input)
	}
	if _, ok := input["id"]; ok {
		t.Fatalf("expected input.id to be omitted and id sent as top-level variable")
	}

	externalURLs := arrayFromMap(t, input, "externalUrls")
	if len(externalURLs) != 2 {
		t.Fatalf("expected 2 externalUrls entries, got %d", len(externalURLs))
	}
	if got := stringFromMap(t, externalURLs[0], "label"); got != "Runner Session" {
		t.Fatalf("expected first externalUrls label Runner Session, got %q", got)
	}
	if got := stringFromMap(t, externalURLs[0], "url"); got != "https://example.test/sessions/session-1" {
		t.Fatalf("expected first externalUrls url to round-trip, got %q", got)
	}
}

func TestAgentActivityClientUpdateSessionExternalURLsDeduplicatesByURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := graphQLRequest{}
		decodeGraphQLRequest(t, r, &request)

		input := mapFromMap(t, request.Variables, "input")
		externalURLs := arrayFromMap(t, input, "externalUrls")
		if len(externalURLs) != 1 {
			t.Fatalf("expected externalUrls deduplicated by URL to one entry, got %d", len(externalURLs))
		}
		if got := stringFromMap(t, externalURLs[0], "label"); got != "Runner Session" {
			t.Fatalf("expected first label to win after dedupe, got %q", got)
		}
		if got := stringFromMap(t, externalURLs[0], "url"); got != "https://example.test/sessions/session-1" {
			t.Fatalf("expected deduped URL to round-trip, got %q", got)
		}

		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentSessionUpdate": map[string]any{
					"success": true,
					"agentSession": map[string]any{
						"id": "session-1",
					},
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentActivityClient(AgentActivityClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.UpdateSessionExternalURLs(context.Background(), AgentSessionExternalURLsInput{
		AgentSessionID: "session-1",
		ExternalURLs: []ExternalURL{
			{Label: "Runner Session", URL: "https://example.test/sessions/session-1"},
			{Label: "Alternate Label", URL: "https://example.test/sessions/session-1"},
		},
	})
	if err != nil {
		t.Fatalf("update session external urls: %v", err)
	}
}

type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

func decodeGraphQLRequest(t *testing.T, r *http.Request, req *graphQLRequest) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		t.Fatalf("decode graphql request: %v", err)
	}
}

func writeGraphQLResponse(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode graphql response: %v", err)
	}
}

func decodeMutationInput(t *testing.T, req graphQLRequest) map[string]interface{} {
	t.Helper()
	raw, ok := req.Variables["input"]
	if !ok {
		t.Fatalf("expected variables.input to be present")
	}
	input, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected variables.input to be object, got %T", raw)
	}
	return input
}

func mapFromMap(t *testing.T, m map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("expected key %q", key)
	}
	got, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected key %q to be object, got %T", key, raw)
	}
	return got
}

func stringFromMap(t *testing.T, m map[string]interface{}, key string) string {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("expected key %q", key)
	}
	got, ok := raw.(string)
	if !ok {
		t.Fatalf("expected key %q to be string, got %T", key, raw)
	}
	return got
}

func arrayFromMap(t *testing.T, m map[string]interface{}, key string) []map[string]interface{} {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("expected key %q", key)
	}
	items, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("expected key %q to be array, got %T", key, raw)
	}
	out := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("expected array item in %q to be object, got %T", key, item)
		}
		out = append(out, entry)
	}
	return out
}
