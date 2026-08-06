package arcanum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultAPIBaseURL is the production Arcanum API endpoint. It is NOT a
	// default applied automatically: NewAPIClient requires the base URL to be
	// passed explicitly so that tests and local runs can never silently mutate
	// production. Only the real daemon wiring should reference this constant.
	DefaultAPIBaseURL       = "https://a.yandex-team.ru/api"
	arcTokenEnv             = "ARC_TOKEN"
	maxAPIErrorBodyExcerpt  = 512
	defaultAPIClientTimeout = 15 * time.Second
)

type APIHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type APITokenSource func(context.Context) (string, error)

type APIClientConfig struct {
	BaseURL     string
	HTTPClient  APIHTTPClient
	TokenSource APITokenSource
}

type APIClient struct {
	baseURL     string
	httpClient  APIHTTPClient
	tokenSource APITokenSource
}

func NewAPIClient(cfg APIClientConfig) (*APIClient, error) {
	baseURL, err := normalizeAPIBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultAPIClientTimeout}
	}

	tokenSource := cfg.TokenSource
	if tokenSource == nil {
		tokenSource = DefaultAPITokenSource
	}

	return &APIClient{
		baseURL:     baseURL,
		httpClient:  httpClient,
		tokenSource: tokenSource,
	}, nil
}

func DefaultAPITokenSource(context.Context) (string, error) {
	if token := strings.TrimSpace(os.Getenv(arcTokenEnv)); token != "" {
		return token, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory for Arcanum token: %w", err)
	}

	path := filepath.Join(home, ".arc", "token")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Arcanum token from %s: %w", path, err)
	}

	line, _, _ := strings.Cut(string(raw), "\n")
	token := strings.TrimSpace(line)
	if token == "" {
		return "", fmt.Errorf("read Arcanum token from %s: first line is empty", path)
	}
	return token, nil
}

func (c *APIClient) GetJSON(ctx context.Context, path string, responseBody any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, responseBody)
}

func (c *APIClient) PostJSON(ctx context.Context, path string, requestBody any, responseBody any) error {
	return c.doJSON(ctx, http.MethodPost, path, requestBody, responseBody)
}

// PatchJSON sends a PATCH to the Arcanum API. Mirrors PostJSON/GetJSON as a
// one-liner over doJSON. The resolve client uses POST today (see
// resolve_client.go); PATCH is the documented fallback candidate for the
// resolve endpoint and is exposed here so it is available once confirmed.
func (c *APIClient) PatchJSON(ctx context.Context, path string, requestBody any, responseBody any) error {
	return c.doJSON(ctx, http.MethodPatch, path, requestBody, responseBody)
}

func (c *APIClient) doJSON(ctx context.Context, method string, path string, requestBody any, responseBody any) error {
	if c == nil {
		return errors.New("Arcanum API client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestURL, err := c.buildURL(path)
	if err != nil {
		return err
	}

	var body io.Reader = http.NoBody
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal Arcanum API request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("build Arcanum API request: %w", err)
	}

	token, err := c.tokenSource(ctx)
	if err != nil {
		return fmt.Errorf("load Arcanum API token: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("Arcanum API token is required")
	}

	req.Header.Set("Authorization", "OAuth "+token)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send Arcanum API request: %w", err)
	}
	if resp == nil {
		return errors.New("send Arcanum API request: nil response")
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read Arcanum API response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &APIStatusError{
			Method:     method,
			Path:       path,
			StatusCode: resp.StatusCode,
			Body:       apiErrorBodyExcerpt(raw, resp.StatusCode),
		}
	}

	if responseBody == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, responseBody); err != nil {
		return fmt.Errorf("decode Arcanum API response: %w", err)
	}
	return nil
}

// APIStatusError is a non-2xx Arcanum API response. It carries the status code
// so callers can branch on it (e.g. treat 404 as a deleted review request)
// instead of matching error strings.
type APIStatusError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *APIStatusError) Error() string {
	return fmt.Sprintf(
		"Arcanum API request %s %s failed: status %d: %s",
		e.Method,
		e.Path,
		e.StatusCode,
		e.Body,
	)
}

// IsAPINotFound reports whether an error is an Arcanum API 404 response.
func IsAPINotFound(err error) bool {
	var statusErr *APIStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound
}

func normalizeAPIBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		// No implicit production fallback: an unconfigured base URL is an error
		// rather than a silent prod target. This is the guardrail that stops
		// tests/dev runs from posting to real Arcanum reviews.
		return "", errors.New("Arcanum API base URL is required (no implicit production default)")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse Arcanum API base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("Arcanum API base URL must be an absolute URL")
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func (c *APIClient) buildURL(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("Arcanum API request path is required")
	}

	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse Arcanum API base URL: %w", err)
	}

	relative, err := url.Parse(strings.TrimLeft(trimmed, "/"))
	if err != nil {
		return "", fmt.Errorf("parse Arcanum API request path: %w", err)
	}
	if relative.IsAbs() {
		return "", errors.New("Arcanum API request path must be relative")
	}

	base.Path = strings.TrimRight(base.Path, "/") + "/"
	return base.ResolveReference(relative).String(), nil
}

func apiErrorBodyExcerpt(raw []byte, statusCode int) string {
	excerpt := strings.TrimSpace(string(raw))
	if excerpt == "" {
		return http.StatusText(statusCode)
	}
	if len(excerpt) <= maxAPIErrorBodyExcerpt {
		return excerpt
	}
	return strings.TrimSpace(excerpt[:maxAPIErrorBodyExcerpt]) + "..."
}
