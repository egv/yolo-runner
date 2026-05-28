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
	"strings"
	"time"
)

const defaultMaxResponseBytes int64 = 1 << 20

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
	if c == nil {
		return errors.New("startrek client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	requestURL, err := c.buildURL(requestPath)
	if err != nil {
		return err
	}

	var body io.Reader = http.NoBody
	if requestBody != nil {
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal startrek request body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("build startrek request: %w", err)
	}
	req.Header.Set("Authorization", "OAuth "+c.token)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send startrek request: %w", err)
	}
	if resp == nil {
		return errors.New("send startrek request: nil response")
	}
	defer resp.Body.Close()

	raw, err := readBounded(resp.Body, c.maxResponseBytes)
	if err != nil {
		return fmt.Errorf("read startrek response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg := startrekErrorMessage(raw)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("startrek request %s %s: http %d: %s", method, requestPath, resp.StatusCode, msg)
	}

	if responseBody == nil || len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, responseBody); err != nil {
		return fmt.Errorf("decode startrek response: %w", err)
	}

	return nil
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
