package arcanum

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

type arcanumHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

const (
	defaultArcanumAPIBaseURL = "https://a.yandex-team.ru/api/v1/public"
	arcanumMaxResponseBytes  = 10 << 20
)

var (
	arcanumAPIBaseURL                 = defaultArcanumAPIBaseURL
	arcanumHTTPClient arcanumHTTPDoer = &http.Client{Timeout: 15 * time.Second}
)

func FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return arcreview.PRRuntimeState{}, fmt.Errorf("workspace is required")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return arcreview.PRRuntimeState{}, fmt.Errorf("PR ID is required")
	}

	detailsOutput, err := RunWorkspaceArc(ctx, workspace, "pr", "status", "--json", prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	details, err := ParsePRDetailsJSON(detailsOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR details: %w", err)
	}

	commentsOutput, err := fetchArcanumPRCommentsJSON(ctx, workspace, prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	comments, err := ParsePRCommentsJSON(commentsOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR comments: %w", err)
	}

	diffOutput, err := RunWorkspaceArc(ctx, workspace, "pr", "changes", prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	changedFiles, err := ParsePRChangedFilesDiff(diffOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR changed files: %w", err)
	}

	checks, err := ParsePRChecksJSON(detailsOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR checks: %w", err)
	}

	return arcreview.NormalizePRRuntimeState(details, comments, changedFiles, checks), nil
}

func fetchArcanumPRCommentsJSON(ctx context.Context, workspace string, prID string) ([]byte, error) {
	tokenOutput, err := RunWorkspaceArc(ctx, workspace, "token", "show", "--json")
	if err != nil {
		return nil, fmt.Errorf("fetch Arcanum token: %w", err)
	}
	token, err := arcanumTokenFromArcOutput(tokenOutput)
	if err != nil {
		return nil, err
	}

	commentsURL := arcanumPRCommentsURL(arcanumAPIBaseURL, prID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commentsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build Arcanum comments request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "OAuth "+token)

	client := arcanumHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Arcanum comments: %w", err)
	}
	defer resp.Body.Close()

	body, err := readLimitedArcanumResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read Arcanum comments: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch Arcanum comments: GET %s returned %s: %s", commentsURL, resp.Status, compactResponseBody(body))
	}
	return body, nil
}

func arcanumTokenFromArcOutput(data []byte) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil && len(object) > 0 {
		if token := firstScalar(object, "token", "access_token", "accessToken", "oauth_token", "oauthToken"); token != "" {
			return token, nil
		}
		if nested := rawObject(object["data"]); nested != nil {
			if token := firstScalar(nested, "token", "access_token", "accessToken", "oauth_token", "oauthToken"); token != "" {
				return token, nil
			}
		}
		return "", fmt.Errorf("arc token show --json did not contain a token")
	}

	var scalar string
	if err := json.Unmarshal(data, &scalar); err == nil {
		if token := strings.TrimSpace(scalar); token != "" {
			return token, nil
		}
	}

	for _, line := range strings.Split(string(data), "\n") {
		if token := strings.TrimSpace(line); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("arc token show returned an empty token")
}

func arcanumPRCommentsURL(baseURL string, prID string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") +
		"/review-requests/" + url.PathEscape(strings.TrimSpace(prID)) + "/comments"
}

func readLimitedArcanumResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, arcanumMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > arcanumMaxResponseBytes {
		return nil, fmt.Errorf("response exceeded %d bytes", arcanumMaxResponseBytes)
	}
	return data, nil
}

func compactResponseBody(body []byte) string {
	const maxBody = 512
	text := strings.TrimSpace(string(body))
	if len(text) > maxBody {
		return text[:maxBody] + "..."
	}
	return text
}
