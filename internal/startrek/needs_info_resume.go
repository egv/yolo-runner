package startrek

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type NeedsInfoResumeInput struct {
	QueueKey       string
	ReadyLabel     string
	Marker         string
	AgentAuthorIDs []string
}

func (b *StorageBackend) ResumeNeedsInfoTasks(ctx context.Context, input NeedsInfoResumeInput) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}

	queueKey := strings.TrimSpace(input.QueueKey)
	if queueKey == "" {
		return nil, errors.New("startrek queue key is required")
	}
	marker := fallbackText(input.Marker, defaultNeedsInfoMarker)

	// needs-info is the native needInfo workflow status now; find candidates
	// by status, not by a tag.
	issues, err := b.searchIssuesByStatus(ctx, queueKey, defaultNeedsInfoStatusKey)
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, nil
	}

	resumed := make([]string, 0)
	for _, issue := range issues {
		issueID := strings.TrimSpace(issue.ID)
		if issueID == "" {
			continue
		}

		comments, err := b.client.GetIssueComments(ctx, issueID)
		if err != nil {
			return nil, fmt.Errorf("get startrek comments for needs-info issue %q: %w", issueID, err)
		}
		markerCreatedAt := latestNeedsInfoMarkerCreatedAt(comments, marker)
		if !ShouldResumeNeedsInfoWait(NeedsInfoWaitState{
			MarkerCreatedAt: markerCreatedAt,
			Author:          issue.Author,
			Assignee:        issue.Assignee,
			AgentAuthorIDs:  input.AgentAuthorIDs,
		}, comments) {
			continue
		}

		// Resume flips needInfo -> open via the configured ready transition
		// (e.g. provide_info) and re-applies the ready discovery tag so the
		// next discovery poll picks the issue back up.
		if err := b.SetTaskStatus(ctx, issueID, contracts.TaskStatusOpen); err != nil {
			return nil, fmt.Errorf("transition startrek needs-info issue %q back to open: %w", issueID, err)
		}
		readyLabel := fallbackText(input.ReadyLabel, b.effectiveReadyLabel())
		if err := b.AddLabel(ctx, issueID, readyLabel); err != nil {
			return nil, fmt.Errorf("add startrek ready label to resumed needs-info issue %q: %w", issueID, err)
		}
		resumed = append(resumed, issueID)
	}
	sort.Strings(resumed)
	return resumed, nil
}

func (b *StorageBackend) searchIssuesByStatus(ctx context.Context, queueKey string, status string) ([]Issue, error) {
	page := defaultIssueSearchPage
	perPage := b.searchPerPage
	if perPage <= 0 {
		perPage = defaultIssueSearchPerPage
	}

	issuesByID := map[string]Issue{}
	for {
		result, err := b.client.SearchIssues(ctx, IssueSearchOptions{
			QueueKey: queueKey,
			Status:   status,
			Page:     page,
			PerPage:  perPage,
		})
		if err != nil {
			return nil, fmt.Errorf("search startrek queue %q for status %q: %w", queueKey, status, err)
		}
		for _, issue := range result.Issues {
			issueID := strings.TrimSpace(issue.ID)
			if issueID != "" {
				issuesByID[issueID] = issue
			}
		}
		if result.TotalPages <= page || result.TotalPages <= 0 {
			break
		}
		page++
	}

	ids := make([]string, 0, len(issuesByID))
	for issueID := range issuesByID {
		ids = append(ids, issueID)
	}
	sort.Strings(ids)

	issues := make([]Issue, 0, len(ids))
	for _, issueID := range ids {
		issues = append(issues, issuesByID[issueID])
	}
	return issues, nil
}

func latestNeedsInfoMarkerCreatedAt(comments []IssueComment, marker string) time.Time {
	marker = fallbackText(marker, defaultNeedsInfoMarker)
	needle := markerCommentNeedle(marker)
	var latest time.Time
	for _, comment := range comments {
		if !strings.Contains(comment.Body, needle) {
			continue
		}
		if comment.CreatedAt.After(latest) {
			latest = comment.CreatedAt
		}
	}
	return latest
}
