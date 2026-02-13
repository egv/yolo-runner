package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

const (
	defaultAPIEndpoint   = "https://api.github.com"
	maxProbeResponseSize = 1 << 20
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Owner       string
	Repo        string
	Token       string
	APIEndpoint string
	HTTPClient  HTTPClient
}

type TaskManager struct {
	owner       string
	repo        string
	token       string
	apiEndpoint string
	client      HTTPClient
}

func NewTaskManager(cfg Config) (*TaskManager, error) {
	owner := strings.TrimSpace(cfg.Owner)
	if owner == "" {
		return nil, errors.New("github owner is required")
	}

	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		return nil, errors.New("github repository is required")
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("github auth token is required")
	}

	endpoint := strings.TrimSpace(cfg.APIEndpoint)
	if endpoint == "" {
		endpoint = defaultAPIEndpoint
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	if err := probeRepository(context.Background(), client, endpoint, owner, repo, token); err != nil {
		return nil, fmt.Errorf("github auth validation failed: %w", err)
	}

	return &TaskManager{
		owner:       owner,
		repo:        repo,
		token:       token,
		apiEndpoint: endpoint,
		client:      client,
	}, nil
}

func (m *TaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	return nil, errors.New("github NextTasks is not implemented yet")
}

func (m *TaskManager) GetTask(context.Context, string) (contracts.Task, error) {
	return contracts.Task{}, errors.New("github GetTask is not implemented yet")
}

func (m *TaskManager) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	return errors.New("github SetTaskStatus is not implemented yet")
}

func (m *TaskManager) SetTaskData(context.Context, string, map[string]string) error {
	return errors.New("github SetTaskData is not implemented yet")
}

func probeRepository(ctx context.Context, client HTTPClient, endpoint string, owner string, repo string, token string) error {
	requestURL := strings.TrimRight(endpoint, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("cannot build probe request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeResponseSize))
	if err != nil {
		return fmt.Errorf("cannot read probe response: %w", err)
	}

	var probe struct {
		FullName string `json:"full_name"`
		Message  string `json:"message"`
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &probe); err != nil {
			if resp.StatusCode >= http.StatusBadRequest {
				return fmt.Errorf("probe failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
			return fmt.Errorf("cannot parse probe response: %w", err)
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("probe failed with status %d: %s", resp.StatusCode, firstProbeError(probe.Message, strings.TrimSpace(string(body))))
	}
	if strings.TrimSpace(probe.FullName) == "" {
		return errors.New("probe failed: repository identity missing in response")
	}

	expected := strings.ToLower(owner + "/" + repo)
	if strings.ToLower(strings.TrimSpace(probe.FullName)) != expected {
		return fmt.Errorf("probe failed: expected repository %q, got %q", expected, strings.TrimSpace(probe.FullName))
	}

	return nil
}

func firstProbeError(message string, fallback string) string {
	if strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message)
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "unknown error"
}
