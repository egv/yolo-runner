package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

const (
	defaultGraphQLEndpoint = "https://api.linear.app/graphql"
	maxProbeResponseBytes  = 1 << 20
)

var (
	ErrReadOperationsNotImplemented  = errors.New("linear read operations are not implemented yet")
	ErrWriteOperationsNotImplemented = errors.New("linear write operations are not implemented yet")
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Workspace  string
	Token      string
	Endpoint   string
	HTTPClient HTTPClient
}

type probeGraphQLError struct {
	Message string `json:"message"`
}

type TaskManager struct {
	workspace string
	token     string
	endpoint  string
	client    HTTPClient
}

func NewTaskManager(cfg Config) (*TaskManager, error) {
	workspace := strings.TrimSpace(cfg.Workspace)
	if workspace == "" {
		return nil, errors.New("linear workspace is required")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("linear auth token is required")
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultGraphQLEndpoint
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	if err := probeViewer(context.Background(), client, endpoint, token); err != nil {
		return nil, fmt.Errorf("linear auth validation failed: %w", err)
	}

	return &TaskManager{
		workspace: workspace,
		token:     token,
		endpoint:  endpoint,
		client:    client,
	}, nil
}

func (m *TaskManager) NextTasks(_ context.Context, _ string) ([]contracts.TaskSummary, error) {
	return nil, ErrReadOperationsNotImplemented
}

func (m *TaskManager) GetTask(_ context.Context, _ string) (contracts.Task, error) {
	return contracts.Task{}, ErrReadOperationsNotImplemented
}

func (m *TaskManager) SetTaskStatus(_ context.Context, _ string, _ contracts.TaskStatus) error {
	return ErrWriteOperationsNotImplemented
}

func (m *TaskManager) SetTaskData(_ context.Context, _ string, _ map[string]string) error {
	return ErrWriteOperationsNotImplemented
}

func probeViewer(ctx context.Context, client HTTPClient, endpoint string, token string) error {
	reqBody := struct {
		Query string `json:"query"`
	}{
		Query: "query AuthProbe { viewer { id } }",
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("cannot encode probe request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("cannot build probe request: %w", err)
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeResponseBytes))
	if err != nil {
		return fmt.Errorf("cannot read probe response: %w", err)
	}

	var graphQLResp struct {
		Data struct {
			Viewer *struct {
				ID string `json:"id"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []probeGraphQLError `json:"errors"`
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &graphQLResp); err != nil {
			if resp.StatusCode >= http.StatusBadRequest {
				return fmt.Errorf("probe failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			return fmt.Errorf("cannot parse probe response: %w", err)
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("probe failed with status %d: %s", resp.StatusCode, firstProbeError(graphQLResp.Errors, strings.TrimSpace(string(body))))
	}
	if len(graphQLResp.Errors) > 0 {
		return fmt.Errorf("probe failed: %s", firstProbeError(graphQLResp.Errors, "unknown GraphQL error"))
	}
	if graphQLResp.Data.Viewer == nil || strings.TrimSpace(graphQLResp.Data.Viewer.ID) == "" {
		return errors.New("probe failed: viewer identity missing in response")
	}
	return nil
}

func firstProbeError(errors []probeGraphQLError, fallback string) string {
	for _, entry := range errors {
		msg := strings.TrimSpace(entry.Message)
		if msg != "" {
			return msg
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "unknown error"
}
