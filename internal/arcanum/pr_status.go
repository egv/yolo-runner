package arcanum

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// FetchReviewRequestState returns the lifecycle state of an Arcanum review
// request ("open", "merged", "discarded", ...) using the default production
// API endpoint. Runner-side gates use it to refuse work on closed PRs.
func FetchReviewRequestState(ctx context.Context, prID string) (string, error) {
	client, err := NewAPIClient(APIClientConfig{BaseURL: DefaultAPIBaseURL})
	if err != nil {
		return "", err
	}
	return FetchReviewRequestStateWithClient(ctx, client, prID)
}

// FetchReviewRequestStateWithClient is the testable variant of
// FetchReviewRequestState.
func FetchReviewRequestStateWithClient(ctx context.Context, client *APIClient, prID string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("Arcanum API client is required")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return "", fmt.Errorf("PR ID is required")
	}

	var raw json.RawMessage
	if err := client.GetJSON(ctx, reviewRequestStatePath(prID), &raw); err != nil {
		return "", fmt.Errorf("fetch state for PR %q: %w", prID, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("parse state for PR %q: %w", prID, err)
	}
	body := envelope["data"]
	if len(body) == 0 {
		body = raw
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(body, &item); err != nil {
		return "", fmt.Errorf("parse state for PR %q: %w", prID, err)
	}
	return firstScalar(item, "state", "status"), nil
}

// ReviewRequestStateClosed reports whether a review-request state means the PR
// can no longer accept work (merged or discarded). Unknown or empty states are
// treated as open so an API contract change degrades to the old behavior
// instead of silently cancelling live work.
func ReviewRequestStateClosed(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "merged", "discarded", "closed", "abandoned":
		return true
	}
	return false
}

func reviewRequestStatePath(prID string) string {
	query := url.Values{}
	query.Set("fields", "state")
	return "/v1/review-requests/" + url.PathEscape(prID) + "?" + query.Encode()
}
