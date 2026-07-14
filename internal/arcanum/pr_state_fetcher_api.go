package arcanum

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// FetchPRRuntimeStateWithClient builds a PR runtime state entirely from the
// Arcanum HTTP API — no Arc workspace, mount, or per-PR checkout lock is
// involved. Writeback paths must use this instead of preparing a checkout:
// a checkout acquires the per-PR lock, and when an implement worker holds it
// for the duration of an agent run, a source blocked on that lock stops
// polling entirely.
//
// ChangedFiles are not populated: the list requires a workspace (`arc pr
// changes`) and no writeback consumer reads them.
func FetchPRRuntimeStateWithClient(ctx context.Context, apiClient *APIClient, prID string) (arcreview.PRRuntimeState, error) {
	if apiClient == nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("Arcanum API client is required for PR runtime state")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return arcreview.PRRuntimeState{}, fmt.Errorf("PR ID is required")
	}

	var raw json.RawMessage
	if err := apiClient.GetJSON(ctx, apiReviewRequestStatePath(prID), &raw); err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("fetch review request %q: %w", prID, err)
	}
	details, checks, err := parseAPIReviewRequestState(raw)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse review request %q: %w", prID, err)
	}
	details.ID = prID

	comments, err := FetchPRComments(ctx, prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	return arcreview.NormalizePRRuntimeState(details, comments, nil, checks), nil
}

func apiReviewRequestStatePath(prID string) string {
	query := url.Values{}
	query.Set("fields", "id,summary,description,author(name),state,vcs(from_branch,to_branch),active_diff_set(id),checks(system,type,status,required)")
	return "/v1/review-requests/" + url.PathEscape(prID) + "?" + query.Encode()
}

func parseAPIReviewRequestState(raw json.RawMessage) (arcreview.PRDetails, []arcreview.PRCheck, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return arcreview.PRDetails{}, nil, err
	}
	body := envelope["data"]
	if len(body) == 0 {
		body = raw
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(body, &item); err != nil {
		return arcreview.PRDetails{}, nil, err
	}

	summary := firstScalar(item, "summary")
	details := arcreview.PRDetails{
		Title:        summary,
		Author:       firstPerson(item, "author"),
		Status:       firstScalar(item, "state", "status"),
		Revision:     nestedScalar(item, "active_diff_set", "id"),
		Branch:       nestedScalar(item, "vcs", "from_branch"),
		SourceBranch: nestedScalar(item, "vcs", "from_branch"),
		TargetBranch: nestedScalar(item, "vcs", "to_branch"),
		Description:  firstScalar(item, "description"),
	}

	var checks []arcreview.PRCheck
	if rawChecks := item["checks"]; len(rawChecks) > 0 && string(rawChecks) != "null" {
		var checkItems []map[string]json.RawMessage
		if err := json.Unmarshal(rawChecks, &checkItems); err != nil {
			return arcreview.PRDetails{}, nil, fmt.Errorf("parse checks: %w", err)
		}
		for _, checkItem := range checkItems {
			name := strings.TrimSpace(strings.Join(compactStrings([]string{
				firstScalar(checkItem, "system"),
				firstScalar(checkItem, "type"),
			}), ": "))
			checks = append(checks, arcreview.PRCheck{
				Name:   name,
				Status: firstScalar(checkItem, "status"),
			})
		}
	}
	return details, checks, nil
}
