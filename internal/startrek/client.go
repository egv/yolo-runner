package startrek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultMaxResponseBytes int64 = 1 << 20

const (
	defaultIssueSearchPage    = 1
	defaultIssueSearchPerPage = 50
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	Endpoint         string
	Token            string
	HTTPClient       HTTPClient
	MaxResponseBytes int64
}

type Client struct {
	endpoint         string
	token            string
	httpClient       HTTPClient
	maxResponseBytes int64
}

type IssueSearchOptions struct {
	QueueKey   string
	ReadyLabel string
	Page       int
	PerPage    int
}

type IssueSearchPage struct {
	Issues     []Issue
	Page       int
	PerPage    int
	TotalCount int
	TotalPages int
}

type Issue struct {
	ID        string
	Title     string
	Labels    []string
	Author    IssueAuthor
	UpdatedAt time.Time
}

type IssueAuthor struct {
	ID      string
	Display string
}

func NewClient(cfg Config) (*Client, error) {
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("startrek token is required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}

	return &Client{
		endpoint:         endpoint,
		token:            token,
		httpClient:       httpClient,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

func (c *Client) DoJSON(ctx context.Context, method string, requestPath string, requestBody any, responseBody any) error {
	_, err := c.doJSON(ctx, method, requestPath, requestBody, responseBody)
	return err
}

func (c *Client) SearchIssues(ctx context.Context, opts IssueSearchOptions) (IssueSearchPage, error) {
	queueKey := strings.TrimSpace(opts.QueueKey)
	if queueKey == "" {
		return IssueSearchPage{}, errors.New("startrek issue search queue key is required")
	}
	readyLabel := strings.TrimSpace(opts.ReadyLabel)
	if readyLabel == "" {
		return IssueSearchPage{}, errors.New("startrek issue search ready label is required")
	}

	page := opts.Page
	if page <= 0 {
		page = defaultIssueSearchPage
	}
	perPage := opts.PerPage
	if perPage <= 0 {
		perPage = defaultIssueSearchPerPage
	}

	requestBody := map[string]any{
		"filter": map[string]any{
			"queue": queueKey,
			"tags":  readyLabel,
		},
	}

	var rawIssues []startrekIssueSearchItem
	headers, err := c.doJSON(ctx, http.MethodPost, issueSearchPath(page, perPage), requestBody, &rawIssues)
	if err != nil {
		return IssueSearchPage{}, err
	}

	issues := make([]Issue, 0, len(rawIssues))
	for _, raw := range rawIssues {
		issue, err := mapIssue(raw)
		if err != nil {
			return IssueSearchPage{}, err
		}
		issues = append(issues, issue)
	}

	totalCount, err := responseHeaderInt(headers, "X-Total-Count")
	if err != nil {
		return IssueSearchPage{}, err
	}
	totalPages, err := responseHeaderInt(headers, "X-Total-Pages")
	if err != nil {
		return IssueSearchPage{}, err
	}

	return IssueSearchPage{
		Issues:     issues,
		Page:       page,
		PerPage:    perPage,
		TotalCount: totalCount,
		TotalPages: totalPages,
	}, nil
}

func (c *Client) doJSON(ctx context.Context, method string, requestPath string, requestBody any, responseBody any) (http.Header, error) {
	if c == nil {
		return nil, errors.New("startrek client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestURL, err := c.buildURL(requestPath)
	if err != nil {
		return nil, err
	}

	var body io.Reader = http.NoBody
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("marshal startrek request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("build startrek request: %w", err)
	}
	req.Header.Set("Authorization", "OAuth "+c.token)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send startrek request: %w", err)
	}
	if resp == nil {
		return nil, errors.New("send startrek request: nil response")
	}
	defer resp.Body.Close()

	raw, err := readBounded(resp.Body, c.maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read startrek response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg := startrekErrorMessage(raw)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("startrek request %s %s: http %d: %s", method, requestPath, resp.StatusCode, msg)
	}

	if responseBody == nil || len(strings.TrimSpace(string(raw))) == 0 {
		return resp.Header.Clone(), nil
	}
	if err := json.Unmarshal(raw, responseBody); err != nil {
		return nil, fmt.Errorf("decode startrek response: %w", err)
	}

	return resp.Header.Clone(), nil
}

func normalizeEndpoint(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("startrek endpoint is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse startrek endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("startrek endpoint must be an absolute URL")
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func (c *Client) buildURL(requestPath string) (string, error) {
	trimmed := strings.TrimSpace(requestPath)
	if trimmed == "" {
		return "", errors.New("startrek request path is required")
	}

	base, err := url.Parse(c.endpoint)
	if err != nil {
		return "", fmt.Errorf("parse startrek endpoint: %w", err)
	}

	relative, err := url.Parse(strings.TrimLeft(trimmed, "/"))
	if err != nil {
		return "", fmt.Errorf("parse startrek request path: %w", err)
	}
	if relative.IsAbs() {
		return "", errors.New("startrek request path must be relative")
	}

	base.Path = strings.TrimRight(base.Path, "/") + "/"
	return base.ResolveReference(relative).String(), nil
}

type startrekIssueSearchItem struct {
	ID        string              `json:"id"`
	Key       string              `json:"key"`
	Summary   string              `json:"summary"`
	Tags      []string            `json:"tags"`
	CreatedBy startrekIssueAuthor `json:"createdBy"`
	UpdatedAt string              `json:"updatedAt"`
}

type startrekIssueAuthor struct {
	ID      string `json:"id"`
	Display string `json:"display"`
}

func issueSearchPath(page int, perPage int) string {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("perPage", strconv.Itoa(perPage))
	return "issues/_search?" + query.Encode()
}

func mapIssue(raw startrekIssueSearchItem) (Issue, error) {
	updatedAt, err := parseStartrekTime(raw.UpdatedAt)
	if err != nil {
		return Issue{}, fmt.Errorf("parse updatedAt for issue %q: %w", issueID(raw), err)
	}

	return Issue{
		ID:        issueID(raw),
		Title:     strings.TrimSpace(raw.Summary),
		Labels:    normalizedLabels(raw.Tags),
		Author:    IssueAuthor{ID: strings.TrimSpace(raw.CreatedBy.ID), Display: strings.TrimSpace(raw.CreatedBy.Display)},
		UpdatedAt: updatedAt,
	}, nil
}

func issueID(raw startrekIssueSearchItem) string {
	if key := strings.TrimSpace(raw.Key); key != "" {
		return key
	}
	return strings.TrimSpace(raw.ID)
}

func normalizedLabels(raw []string) []string {
	labels := make([]string, 0, len(raw))
	for _, label := range raw {
		label = strings.TrimSpace(label)
		if label != "" {
			labels = append(labels, label)
		}
	}
	return labels
}

func parseStartrekTime(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, nil
	}

	for _, layout := range []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("unsupported timestamp %q", trimmed)
}

func responseHeaderInt(headers http.Header, name string) (int, error) {
	raw := strings.TrimSpace(headers.Get(name))
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid startrek response header %s=%q", name, raw)
	}
	return value, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return raw, nil
}

func startrekErrorMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return trimmed
	}

	messages := make([]string, 0, 4)
	for _, key := range []string{"message", "error", "error_description", "description"} {
		if msg, ok := decoded[key].(string); ok {
			msg = strings.TrimSpace(msg)
			if msg != "" {
				messages = append(messages, msg)
			}
		}
	}

	messages = append(messages, errorMessages(decoded["errors"])...)
	if len(messages) == 0 {
		return trimmed
	}
	return strings.Join(messages, "; ")
}

func errorMessages(value any) []string {
	switch typed := value.(type) {
	case []any:
		messages := make([]string, 0, len(typed))
		for _, entry := range typed {
			messages = append(messages, errorMessages(entry)...)
		}
		return messages
	case map[string]any:
		messages := make([]string, 0, len(typed))
		for _, key := range []string{"message", "error", "description"} {
			if msg, ok := typed[key].(string); ok {
				msg = strings.TrimSpace(msg)
				if msg != "" {
					messages = append(messages, msg)
				}
			}
		}
		return messages
	case string:
		if msg := strings.TrimSpace(typed); msg != "" {
			return []string{msg}
		}
	}
	return nil
}
