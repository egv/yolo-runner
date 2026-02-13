package linear

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const createAgentActivityMutation = `
mutation createAgentActivity($input: AgentActivityCreateInput!) {
  agentActivityCreate(input: $input) {
    success
    agentActivity {
      id
    }
  }
}
`

type AgentActivityClientConfig struct {
	Endpoint   string
	Token      string
	HTTPClient *http.Client
}

type AgentActivityClient struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

type ThoughtActivityInput struct {
	AgentSessionID string
	Body           string
	IdempotencyKey string
}

type ActionActivityInput struct {
	AgentSessionID string
	Action         string
	Parameter      string
	Result         *string
	IdempotencyKey string
}

type ResponseActivityInput struct {
	AgentSessionID string
	Body           string
	IdempotencyKey string
}

func NewAgentActivityClient(config AgentActivityClientConfig) (*AgentActivityClient, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("agent activity endpoint is required")
	}

	token := strings.TrimSpace(config.Token)
	if token == "" {
		return nil, fmt.Errorf("agent activity token is required")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &AgentActivityClient{
		endpoint:   endpoint,
		token:      token,
		httpClient: httpClient,
	}, nil
}

func (c *AgentActivityClient) EmitThought(ctx context.Context, input ThoughtActivityInput) (string, error) {
	if err := validateActivityBaseInput(input.AgentSessionID, input.IdempotencyKey); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Body) == "" {
		return "", ErrActivityBodyRequired
	}

	return c.createActivity(ctx, input.AgentSessionID, input.IdempotencyKey, map[string]any{
		"type": string(AgentActivityContentTypeThought),
		"body": input.Body,
	})
}

func (c *AgentActivityClient) EmitAction(ctx context.Context, input ActionActivityInput) (string, error) {
	if err := validateActivityBaseInput(input.AgentSessionID, input.IdempotencyKey); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Action) == "" {
		return "", ErrActionLabelRequired
	}
	if strings.TrimSpace(input.Parameter) == "" {
		return "", ErrActionParameterRequired
	}

	content := map[string]any{
		"type":      string(AgentActivityContentTypeAction),
		"action":    input.Action,
		"parameter": input.Parameter,
	}
	if input.Result != nil {
		content["result"] = *input.Result
	}

	return c.createActivity(ctx, input.AgentSessionID, input.IdempotencyKey, content)
}

func (c *AgentActivityClient) EmitResponse(ctx context.Context, input ResponseActivityInput) (string, error) {
	if err := validateActivityBaseInput(input.AgentSessionID, input.IdempotencyKey); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Body) == "" {
		return "", ErrActivityBodyRequired
	}

	return c.createActivity(ctx, input.AgentSessionID, input.IdempotencyKey, map[string]any{
		"type": string(AgentActivityContentTypeResponse),
		"body": input.Body,
	})
}

func validateActivityBaseInput(agentSessionID string, idempotencyKey string) error {
	if strings.TrimSpace(agentSessionID) == "" {
		return fmt.Errorf("agent session id is required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return fmt.Errorf("idempotency key is required")
	}
	return nil
}

func (c *AgentActivityClient) createActivity(ctx context.Context, agentSessionID string, idempotencyKey string, content map[string]any) (string, error) {
	if c == nil {
		return "", fmt.Errorf("agent activity client is nil")
	}

	input := map[string]any{
		"agentSessionId": agentSessionID,
		"content":        content,
		"id":             activityIDFromIdempotencyKey(idempotencyKey),
	}
	body := graphqlMutationRequest{
		Query: createAgentActivityMutation,
		Variables: map[string]any{
			"input": input,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal agent activity mutation: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build agent activity mutation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send agent activity mutation: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read agent activity mutation response: %w", err)
	}

	decoded := createActivityResponse{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", fmt.Errorf("decode agent activity mutation response: %w", err)
		}
	}

	if len(decoded.Errors) > 0 {
		return "", fmt.Errorf("agent activity mutation graphql errors: %s", joinGraphQLErrors(decoded.Errors))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("agent activity mutation http %d: %s", resp.StatusCode, msg)
	}

	result := decoded.Data.AgentActivityCreate
	if !result.Success {
		return "", fmt.Errorf("agent activity mutation unsuccessful")
	}
	if result.AgentActivity == nil || strings.TrimSpace(result.AgentActivity.ID) == "" {
		return "", fmt.Errorf("agent activity mutation missing activity id")
	}

	return result.AgentActivity.ID, nil
}

func activityIDFromIdempotencyKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	id := sum[:16]

	// Set UUIDv4 variant/version bits to keep generated IDs compatible.
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func joinGraphQLErrors(errors []graphQLError) string {
	messages := make([]string, 0, len(errors))
	for _, gqlErr := range errors {
		msg := strings.TrimSpace(gqlErr.Message)
		if msg == "" {
			continue
		}
		messages = append(messages, msg)
	}
	if len(messages) == 0 {
		return "unknown graphql error"
	}
	return strings.Join(messages, "; ")
}

type graphqlMutationRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type createActivityResponse struct {
	Data struct {
		AgentActivityCreate createActivityResult `json:"agentActivityCreate"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type createActivityResult struct {
	Success       bool `json:"success"`
	AgentActivity *struct {
		ID string `json:"id"`
	} `json:"agentActivity"`
}

type graphQLError struct {
	Message string `json:"message"`
}
