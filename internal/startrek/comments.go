package startrek

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	splitCreatedCommentMarker     = "split-created"
	failureCommentMarker          = "failure"
	parentPRCreatedCommentMarker  = "parent-pr-created"
	defaultFailureCommentReason   = "task failed"
	progressCommentSubtasksHeader = "Subtasks:"
)

type progressCommentTracker interface {
	CreateIssueComment(ctx context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error)
}

func PostSplitCreatedComment(ctx context.Context, tracker progressCommentTracker, parentID string, subtaskIDs []string) error {
	body, err := splitCreatedCommentBody(subtaskIDs)
	if err != nil {
		return err
	}
	return postProgressComment(ctx, tracker, parentID, splitCreatedCommentMarker, body)
}

func PostFailureComment(ctx context.Context, tracker progressCommentTracker, issueID string, reason string) error {
	return postProgressComment(ctx, tracker, issueID, failureCommentMarker, failureCommentBody(reason))
}

func PostParentPRCreatedComment(ctx context.Context, tracker progressCommentTracker, parentID string, prURL string, subtaskIDs []string) error {
	body, err := parentPRCreatedCommentBody(prURL, subtaskIDs)
	if err != nil {
		return err
	}
	return postProgressComment(ctx, tracker, parentID, parentPRCreatedCommentMarker, body)
}

func postProgressComment(ctx context.Context, tracker progressCommentTracker, issueID string, marker string, body string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if tracker == nil {
		return errors.New("startrek comment tracker is required")
	}

	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return errors.New("startrek issue id is required")
	}

	if _, err := tracker.CreateIssueComment(ctx, issueID, IssueCommentCreateOptions{
		Body:   body,
		Marker: marker,
	}); err != nil {
		return fmt.Errorf("post startrek %s comment on issue %q: %w", marker, issueID, err)
	}
	return nil
}

func splitCreatedCommentBody(subtaskIDs []string) (string, error) {
	ids := normalizedSplitMarkerSubtaskIDs(subtaskIDs)
	if len(ids) == 0 {
		return "", errors.New("startrek split-created comment subtask ids are required")
	}

	var b strings.Builder
	b.WriteString("### Split created\n\n")
	writeProgressCommentSubtasks(&b, progressCommentSubtasksHeader, ids)
	return b.String(), nil
}

func failureCommentBody(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = defaultFailureCommentReason
	}

	var b strings.Builder
	b.WriteString("### Task failed\n\n")
	b.WriteString("Reason:\n")
	b.WriteString(reason)
	return b.String()
}

func parentPRCreatedCommentBody(prURL string, subtaskIDs []string) (string, error) {
	prURL = strings.TrimSpace(prURL)
	if prURL == "" {
		return "", errors.New("startrek parent PR-created comment PR URL is required")
	}

	ids := normalizedSplitMarkerSubtaskIDs(subtaskIDs)
	if len(ids) == 0 {
		return "", errors.New("startrek parent PR-created comment subtask ids are required")
	}

	var b strings.Builder
	b.WriteString("### Parent PR created\n\n")
	b.WriteString("PR: ")
	b.WriteString(prURL)
	b.WriteString("\n\n")
	writeProgressCommentSubtasks(&b, "Completed split subtasks:", ids)
	return b.String(), nil
}

func writeProgressCommentSubtasks(b *strings.Builder, header string, subtaskIDs []string) {
	b.WriteString(header)
	for _, subtaskID := range subtaskIDs {
		b.WriteString("\n- ")
		b.WriteString(subtaskID)
	}
}
