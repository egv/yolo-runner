package startrek

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultNeedsInfoProcessingLabel = "processing"
	defaultNeedsInfoLabel           = "needs-info"
	defaultNeedsInfoMarker          = "needs-info"

	needsInfoMarkerKey          = "needs_info_marker"
	needsInfoMarkerCommentIDKey = "needs_info_marker_comment_id"
	needsInfoMarkerCreatedAtKey = "needs_info_marker_created_at"
)

type NeedsInfoTransitionTracker interface {
	RemoveLabel(ctx context.Context, issueID string, label string) error
	AddLabel(ctx context.Context, issueID string, label string) error
	CreateIssueComment(ctx context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error)
	SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}

type NeedsInfoTransitionService struct {
	Tracker         NeedsInfoTransitionTracker
	ProcessingLabel string
	NeedsInfoLabel  string
	Marker          string
	Clock           func() time.Time
}

type NeedsInfoTransitionInput struct {
	IssueID    string
	Summary    string
	Questions  []string
	SummoneeID string
}

type NeedsInfoTransitionResult struct {
	Comment    IssueComment
	MarkerData map[string]string
}

func (s NeedsInfoTransitionService) Apply(ctx context.Context, input NeedsInfoTransitionInput) (NeedsInfoTransitionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Tracker == nil {
		return NeedsInfoTransitionResult{}, errors.New("startrek needs-info transition tracker is required")
	}

	issueID := strings.TrimSpace(input.IssueID)
	if issueID == "" {
		return NeedsInfoTransitionResult{}, errors.New("startrek issue id is required")
	}

	questions := normalizedNeedsInfoQuestions(input.Questions)
	if len(questions) == 0 {
		return NeedsInfoTransitionResult{}, errors.New("startrek needs-info questions are required")
	}

	processingLabel := fallbackText(s.ProcessingLabel, defaultNeedsInfoProcessingLabel)
	needsInfoLabel := fallbackText(s.NeedsInfoLabel, defaultNeedsInfoLabel)
	marker := fallbackText(s.Marker, defaultNeedsInfoMarker)

	if err := s.Tracker.RemoveLabel(ctx, issueID, processingLabel); err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("remove startrek processing label from issue %q: %w", issueID, err)
	}
	if err := s.Tracker.AddLabel(ctx, issueID, needsInfoLabel); err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("add startrek needs-info label to issue %q: %w", issueID, err)
	}

	body := buildNeedsInfoCommentBody(input.Summary, questions)
	comment, err := s.Tracker.CreateIssueComment(ctx, issueID, IssueCommentCreateOptions{
		Body:     body,
		AuthorID: strings.TrimSpace(input.SummoneeID),
		Marker:   marker,
	})
	if err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("post startrek needs-info questions on issue %q: %w", issueID, err)
	}

	createdAt := comment.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.now()
	}
	markerData := map[string]string{
		needsInfoMarkerKey:          marker,
		needsInfoMarkerCommentIDKey: strings.TrimSpace(comment.ID),
		needsInfoMarkerCreatedAtKey: createdAt.UTC().Format(time.RFC3339Nano),
	}
	if err := s.Tracker.SetTaskData(ctx, issueID, markerData); err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("write startrek needs-info marker data on issue %q: %w", issueID, err)
	}

	return NeedsInfoTransitionResult{
		Comment:    comment,
		MarkerData: markerData,
	}, nil
}

func (s NeedsInfoTransitionService) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func normalizedNeedsInfoQuestions(questions []string) []string {
	normalized := make([]string, 0, len(questions))
	for _, question := range questions {
		question = strings.TrimSpace(question)
		if question != "" {
			normalized = append(normalized, question)
		}
	}
	return normalized
}

func buildNeedsInfoCommentBody(summary string, questions []string) string {
	if needsInfoTextLooksRussian(summary, questions) {
		return buildRussianNeedsInfoCommentBody(summary, questions)
	}

	lines := []string{"Needs more information before yolo-runner can run this task."}

	if summary = strings.TrimSpace(summary); summary != "" {
		lines = append(lines, "", "Summary:", summary)
	}

	lines = append(lines, "", "Questions:")
	for i, question := range questions {
		lines = append(lines, strconv.Itoa(i+1)+". "+question)
	}
	return strings.Join(lines, "\n")
}

func buildRussianNeedsInfoCommentBody(summary string, questions []string) string {
	lines := []string{"Перед запуском yolo-runner нужно уточнить детали."}

	if summary = strings.TrimSpace(summary); summary != "" {
		lines = append(lines, "", "Кратко:", summary)
	}

	lines = append(lines, "", "Вопросы:")
	for i, question := range questions {
		lines = append(lines, strconv.Itoa(i+1)+". "+question)
	}
	return strings.Join(lines, "\n")
}

func needsInfoTextLooksRussian(summary string, questions []string) bool {
	if containsCyrillic(summary) {
		return true
	}
	for _, question := range questions {
		if containsCyrillic(question) {
			return true
		}
	}
	return false
}

func containsCyrillic(value string) bool {
	for _, r := range value {
		if r >= '\u0400' && r <= '\u04FF' {
			return true
		}
	}
	return false
}

func (b *StorageBackend) RemoveLabel(ctx context.Context, issueID string, label string) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	if err := b.client.RemoveLabel(ctx, issueID, label); err != nil {
		return err
	}
	if status, ok := b.taskStatusForLabel(label); ok {
		b.clearStatusOverrideIf(issueID, status)
	}
	return nil
}

func (b *StorageBackend) AddLabel(ctx context.Context, issueID string, label string) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	if err := b.client.AddLabel(ctx, issueID, label); err != nil {
		return err
	}
	if status, ok := b.taskStatusForLabel(label); ok {
		b.recordStatusOverride(issueID, status)
	}
	return nil
}

func (b *StorageBackend) CreateIssueComment(ctx context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error) {
	if b == nil || b.client == nil {
		return IssueComment{}, errors.New("startrek storage backend is not initialized")
	}
	return b.client.CreateIssueComment(ctx, issueID, opts)
}
