package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const updateAgentSessionMutation = `
mutation updateAgentSession($input: AgentSessionUpdateInput!) {
  agentSessionUpdate(input: $input) {
    success
  }
}
`

type AgentSessionClientConfig struct {
	Endpoint   string
	Token      string
	HTTPClient *http.Client
}

type AgentSessionClient struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

type UpdateAgentSessionExternalURLsInput struct {
	AgentSessionID string
	ExternalURLs   []AgentExternalURL
}

func NewAgentSessionClient(config AgentSessionClientConfig) (*AgentSessionClient, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("agent session endpoint is required")
	}

	token := strings.TrimSpace(config.Token)
	if token == "" {
		return nil, fmt.Errorf("agent session token is required")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &AgentSessionClient{
		endpoint:   endpoint,
		token:      token,
		httpClient: httpClient,
	}, nil
}

func (c *AgentSessionClient) SetExternalURLs(ctx context.Context, input UpdateAgentSessionExternalURLsInput) error {
	if c == nil {
		return fmt.Errorf("agent session client is nil")
	}

	sessionID := strings.TrimSpace(input.AgentSessionID)
	if sessionID == "" {
		return fmt.Errorf("agent session id is required")
	}

	urls := normalizeAgentExternalURLs(input.ExternalURLs)
	if len(urls) == 0 {
		return fmt.Errorf("at least one external url is required")
	}

	externalURLs := make([]map[string]string, 0, len(urls))
	for _, entry := range urls {
		externalURLs = append(externalURLs, map[string]string{
			"label": entry.Label,
			"url":   entry.URL,
		})
	}

	body := graphqlMutationRequest{
		Query: updateAgentSessionMutation,
		Variables: map[string]any{
			"input": map[string]any{
				"id":           sessionID,
				"externalUrls": externalURLs,
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal agent session mutation: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build agent session mutation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send agent session mutation: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read agent session mutation response: %w", err)
	}

	decoded := updateAgentSessionResponse{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("decode agent session mutation response: %w", err)
		}
	}

	if len(decoded.Errors) > 0 {
		return fmt.Errorf("agent session mutation graphql errors: %s", joinGraphQLErrors(decoded.Errors))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("agent session mutation http %d: %s", resp.StatusCode, msg)
	}
	if !decoded.Data.AgentSessionUpdate.Success {
		return fmt.Errorf("agent session mutation unsuccessful")
	}

	return nil
}

func normalizeAgentExternalURLs(urls []AgentExternalURL) []AgentExternalURL {
	if len(urls) == 0 {
		return nil
	}

	normalized := make([]AgentExternalURL, 0, len(urls))
	seen := map[string]struct{}{}
	for _, entry := range urls {
		label := strings.TrimSpace(entry.Label)
		value := strings.TrimSpace(entry.URL)
		if label == "" || value == "" {
			continue
		}
		key := strings.ToLower(label) + "\n" + value
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, AgentExternalURL{
			Label: label,
			URL:   value,
		})
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

type updateAgentSessionResponse struct {
	Data struct {
		AgentSessionUpdate struct {
			Success bool `json:"success"`
		} `json:"agentSessionUpdate"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}
