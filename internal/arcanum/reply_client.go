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
	"strings"
	"time"
)

const defaultArcanumEndpoint = "https://arcanum.yandex.net"

type replyHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type ReplyArcanumClient struct {
	Workspace  string
	Endpoint   string
	HTTPClient replyHTTPClient
}

func (c ReplyArcanumClient) PostCommentReply(ctx context.Context, _ string, commentID string, body string) error {
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return errors.New("arcanum comment ID is required")
	}

	token, err := c.arcToken(ctx)
	if err != nil {
		return err
	}

	requestURL, err := commentReplyURL(c.Endpoint, commentID)
	if err != nil {
		return err
	}

	requestBody, err := json.Marshal(map[string]string{"content": body})
	if err != nil {
		return fmt.Errorf("marshal Arcanum comment reply request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("create Arcanum comment reply request: %w", err)
	}
	req.Header.Set("Authorization", "OAuth "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post Arcanum comment reply %s %s: %w", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return fmt.Errorf("read Arcanum comment reply response: %w", readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return arcanumCommentReplyHTTPError(req, resp.StatusCode, responseBody)
	}
	return nil
}

func (c ReplyArcanumClient) arcToken(ctx context.Context) (string, error) {
	stdout, err := RunWorkspaceArc(ctx, c.Workspace, "token", "show", "--json")
	if err != nil {
		return "", err
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &payload); err != nil {
		return "", fmt.Errorf("parse arc token show --json: %w", err)
	}
	token := strings.TrimSpace(payload.Token)
	if token == "" {
		return "", errors.New("arc token show --json returned empty token")
	}
	return token, nil
}

func commentReplyURL(endpoint string, commentID string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = defaultArcanumEndpoint
	}
	requestURL, err := url.JoinPath(endpoint, "api", "v1", "review-requests-comments", commentID, "replies")
	if err != nil {
		return "", fmt.Errorf("build Arcanum comment reply URL: %w", err)
	}
	return requestURL, nil
}

func arcanumCommentReplyHTTPError(req *http.Request, statusCode int, responseBody []byte) error {
	details := arcanumErrorDetails(responseBody)
	if details == "" {
		return fmt.Errorf("post Arcanum comment reply %s %s failed: http %d", req.Method, req.URL.String(), statusCode)
	}
	return fmt.Errorf("post Arcanum comment reply %s %s failed: http %d: %s", req.Method, req.URL.String(), statusCode, details)
}

func arcanumErrorDetails(responseBody []byte) string {
	var payload struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &payload); err == nil {
		var parts []string
		if message := strings.TrimSpace(payload.Message); message != "" {
			parts = append(parts, message)
		}
		for _, apiErr := range payload.Errors {
			if message := strings.TrimSpace(apiErr.Message); message != "" {
				parts = append(parts, message)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ": ")
		}
	}
	return strings.TrimSpace(string(responseBody))
}
