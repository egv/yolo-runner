package arcanum

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ListReviewPRsViaAPI returns open PRs the configured user should monitor.
// User-matched PRs are deduplicated with reviewer matches taking precedence.
func ListReviewPRsViaAPI(ctx context.Context, apiClient *APIClient, user string) ([]PRSummary, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil, nil
	}

	reviewerPRs, err := ListReviewerReviewPRsViaAPI(ctx, apiClient, user)
	if err != nil {
		return nil, err
	}
	authorPRs, err := ListAuthorReviewPRsViaAPI(ctx, apiClient, user)
	if err != nil {
		return nil, err
	}

	return dedupePRSummaries(reviewerPRs, authorPRs), nil
}

// ListReviewerReviewPRsViaAPI returns open PRs where the reviewer list includes the user.
func ListReviewerReviewPRsViaAPI(ctx context.Context, apiClient *APIClient, reviewer string) ([]PRSummary, error) {
	return listReviewPRsViaAPI(ctx, apiClient, "reviewer", reviewer)
}

// ListAuthorReviewPRsViaAPI returns open PRs authored by the user.
func ListAuthorReviewPRsViaAPI(ctx context.Context, apiClient *APIClient, author string) ([]PRSummary, error) {
	return listReviewPRsViaAPI(ctx, apiClient, "author", author)
}

func listReviewPRsViaAPI(ctx context.Context, apiClient *APIClient, filter string, user string) ([]PRSummary, error) {
	if apiClient == nil {
		return nil, fmt.Errorf("Arcanum API client is required")
	}

	user = strings.TrimSpace(user)
	filter = strings.TrimSpace(filter)
	if user == "" {
		return nil, nil
	}

	query := url.Values{}
	query.Set("status", "open")
	if filter != "" {
		query.Set(filter, user)
	}

	var raw json.RawMessage
	path := arcanumReviewRequestsPath
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	if err := apiClient.GetJSON(ctx, path, &raw); err != nil {
		return nil, fmt.Errorf("list review requests via Arcanum API: %w", err)
	}

	all, err := ParsePRListJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Arcanum review request list: %w", err)
	}

	filtered := make([]PRSummary, 0, len(all))
	for _, pr := range all {
		if !isOpenPR(pr) {
			continue
		}
		switch filter {
		case "reviewer":
			if hasReviewer(pr.Reviewers, user) {
				filtered = append(filtered, pr)
			}
		case "author":
			if strings.EqualFold(strings.TrimSpace(pr.Author), user) {
				filtered = append(filtered, pr)
			}
		default:
			filtered = append(filtered, pr)
		}
	}

	return filtered, nil
}

func hasReviewer(reviewers []string, reviewer string) bool {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return false
	}
	for _, candidate := range reviewers {
		if strings.EqualFold(strings.TrimSpace(candidate), reviewer) {
			return true
		}
	}
	return false
}

const arcanumReviewRequestsPath = "/v1/review-requests"
