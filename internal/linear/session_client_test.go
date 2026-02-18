package linear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNewAgentSessionClientValidatesConfig(t *testing.T) {
	t.Run("missing endpoint", func(t *testing.T) {
		_, err := NewAgentSessionClient(AgentSessionClientConfig{
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
		_, err := NewAgentSessionClient(AgentSessionClientConfig{
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

func TestAgentSessionClientSetExternalURLsMutationShape(t *testing.T) {
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
				"agentSessionUpdate": map[string]any{
					"success": true,
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentSessionClient(AgentSessionClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.SetExternalURLs(context.Background(), UpdateAgentSessionExternalURLsInput{
		AgentSessionID: "session-1",
		ExternalURLs: []AgentExternalURL{
			{Label: "Runner Session", URL: "https://example.com/session"},
			{Label: "Runner Log", URL: "https://example.com/log"},
		},
	})
	if err != nil {
		t.Fatalf("set external urls: %v", err)
	}

	expectedQuery := strings.TrimSpace(`
mutation updateAgentSession($id: String!, $input: AgentSessionUpdateInput!) {
  agentSessionUpdate(id: $id, input: $input) {
    success
  }
}
`)
	if got := strings.TrimSpace(captured.Query); got != expectedQuery {
		t.Fatalf("unexpected mutation query\nwant: %q\n got: %q", expectedQuery, got)
	}

	expectedVariables := map[string]any{
		"id": "session-1",
		"input": map[string]any{
			"externalUrls": []any{
				map[string]any{"label": "Runner Session", "url": "https://example.com/session"},
				map[string]any{"label": "Runner Log", "url": "https://example.com/log"},
			},
		},
	}
	if !reflect.DeepEqual(captured.Variables, expectedVariables) {
		t.Fatalf("unexpected mutation variables\nwant: %#v\n got: %#v", expectedVariables, captured.Variables)
	}
}

func TestAgentSessionClientSetExternalURLsDeduplicatesByURL(t *testing.T) {
	captured := graphQLRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeGraphQLRequest(t, r, &captured)
		writeGraphQLResponse(t, w, map[string]any{
			"data": map[string]any{
				"agentSessionUpdate": map[string]any{
					"success": true,
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewAgentSessionClient(AgentSessionClientConfig{
		Endpoint:   server.URL,
		Token:      "linear-token",
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.SetExternalURLs(context.Background(), UpdateAgentSessionExternalURLsInput{
		AgentSessionID: "session-1",
		ExternalURLs: []AgentExternalURL{
			{Label: "Runner Session", URL: "https://example.com/shared"},
			{Label: "Runner Log", URL: "https://example.com/shared"},
			{Label: "Other", URL: "https://example.com/other"},
		},
	})
	if err != nil {
		t.Fatalf("set external urls: %v", err)
	}

	input := decodeMutationInput(t, captured)
	rawURLs, ok := input["externalUrls"].([]any)
	if !ok {
		t.Fatalf("expected input.externalUrls array, got %T", input["externalUrls"])
	}
	if len(rawURLs) != 2 {
		t.Fatalf("expected duplicate URL entries to be deduplicated, got %d entries", len(rawURLs))
	}

	first, ok := rawURLs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first entry object, got %T", rawURLs[0])
	}
	if got := first["label"]; got != "Runner Session" {
		t.Fatalf("expected first duplicate URL label to be kept, got %v", got)
	}
	if got := first["url"]; got != "https://example.com/shared" {
		t.Fatalf("expected duplicate URL value to round-trip, got %v", got)
	}
}
