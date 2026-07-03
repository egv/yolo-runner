package startrek

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultNeedsInfoProcessingLabel = "processing"
	defaultNeedsInfoLabel           = "needs-info"
	defaultNeedsInfoMarker          = "needs-info"
	englishNeedsInfoProxyNotice     = "This comment was posted by yolo-runner by proxy on behalf of the automation."
	russianNeedsInfoProxyNotice     = "Комментарий опубликован yolo-runner через прокси от имени автоматизации."

	needsInfoMarkerKey          = "needs_info_marker"
	needsInfoMarkerCommentIDKey = "needs_info_marker_comment_id"
	needsInfoMarkerCreatedAtKey = "needs_info_marker_created_at"
)

// yoloMarkerCommentPrefix opens every machine-readable yolo-runner marker
// comment; markerCommentNeedle builds the full marker for a given name.
const yoloMarkerCommentPrefix = "<!-- yolo-runner:"

func markerCommentNeedle(marker string) string {
	return yoloMarkerCommentPrefix + marker + " -->"
}

type NeedsInfoTransitionTracker interface {
	GetIssueComments(ctx context.Context, issueID string) ([]IssueComment, error)
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

	comments, err := s.Tracker.GetIssueComments(ctx, issueID)
	if err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("read startrek needs-info comments on issue %q: %w", issueID, err)
	}

	if err := s.Tracker.RemoveLabel(ctx, issueID, processingLabel); err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("remove startrek processing label from issue %q: %w", issueID, err)
	}
	if err := s.Tracker.AddLabel(ctx, issueID, needsInfoLabel); err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("add startrek needs-info label to issue %q: %w", issueID, err)
	}

	if comment, ok := latestNeedsInfoMarkerComment(comments, marker); ok {
		if needsInfoQuestionSetsEqual(needsInfoQuestionSetFromComment(comment, marker), normalizedNeedsInfoQuestionSet(questions)) {
			markerData := s.needsInfoMarkerData(marker, comment)
			if err := s.Tracker.SetTaskData(ctx, issueID, markerData); err != nil {
				return NeedsInfoTransitionResult{}, fmt.Errorf("write startrek needs-info marker data on issue %q: %w", issueID, err)
			}
			return NeedsInfoTransitionResult{
				Comment:    comment,
				MarkerData: markerData,
			}, nil
		}
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

	markerData := s.needsInfoMarkerData(marker, comment)
	if err := s.Tracker.SetTaskData(ctx, issueID, markerData); err != nil {
		return NeedsInfoTransitionResult{}, fmt.Errorf("write startrek needs-info marker data on issue %q: %w", issueID, err)
	}

	return NeedsInfoTransitionResult{
		Comment:    comment,
		MarkerData: markerData,
	}, nil
}

func (s NeedsInfoTransitionService) needsInfoMarkerData(marker string, comment IssueComment) map[string]string {
	createdAt := comment.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.now()
	}
	return map[string]string{
		needsInfoMarkerKey:          marker,
		needsInfoMarkerCommentIDKey: strings.TrimSpace(comment.ID),
		needsInfoMarkerCreatedAtKey: createdAt.UTC().Format(time.RFC3339Nano),
	}
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

var needsInfoQuestionNumberPrefix = regexp.MustCompile(`^\s*(?:\d+[\.)]|[-*])\s*`)

func latestNeedsInfoMarkerComment(comments []IssueComment, marker string) (IssueComment, bool) {
	marker = fallbackText(marker, defaultNeedsInfoMarker)
	prefix := markerCommentNeedle(marker)

	var latest IssueComment
	found := false
	for _, comment := range comments {
		if !strings.HasPrefix(strings.TrimSpace(comment.Body), prefix) {
			continue
		}
		if !found || comment.CreatedAt.After(latest.CreatedAt) || comment.CreatedAt.Equal(latest.CreatedAt) {
			latest = comment
			found = true
		}
	}
	return latest, found
}

func needsInfoQuestionSetFromComment(comment IssueComment, marker string) map[string]struct{} {
	body := strings.TrimSpace(comment.Body)
	prefix := markerCommentNeedle(fallbackText(marker, defaultNeedsInfoMarker))
	if !strings.HasPrefix(body, prefix) {
		return nil
	}
	body = strings.TrimSpace(strings.TrimPrefix(body, prefix))

	questions := make([]string, 0)
	inQuestions := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch strings.ToLower(line) {
		case "questions:", "questions", "вопросы:", "вопросы":
			inQuestions = true
			continue
		}
		if inQuestions {
			questions = append(questions, line)
		}
	}
	return normalizedNeedsInfoQuestionSet(questions)
}

func normalizedNeedsInfoQuestionSet(questions []string) map[string]struct{} {
	normalized := make(map[string]struct{}, len(questions))
	for _, question := range questions {
		question = normalizeNeedsInfoQuestionForDedupe(question)
		if question != "" {
			normalized[question] = struct{}{}
		}
	}
	return normalized
}

func normalizeNeedsInfoQuestionForDedupe(question string) string {
	question = strings.TrimSpace(question)
	for {
		stripped := needsInfoQuestionNumberPrefix.ReplaceAllString(question, "")
		if stripped == question {
			break
		}
		question = strings.TrimSpace(stripped)
	}
	return strings.ToLower(strings.Join(strings.Fields(question), " "))
}

func needsInfoQuestionSetsEqual(left map[string]struct{}, right map[string]struct{}) bool {
	if len(left) == 0 || len(right) == 0 || len(left) != len(right) {
		return false
	}
	for question := range left {
		if _, ok := right[question]; !ok {
			return false
		}
	}
	return true
}

func buildNeedsInfoCommentBody(summary string, questions []string) string {
	if needsInfoTextLooksRussian(summary, questions) {
		return buildRussianNeedsInfoCommentBody(summary, questions)
	}

	lines := []string{
		"Needs more information before yolo-runner can run this task.",
		englishNeedsInfoProxyNotice,
	}

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
	lines := []string{
		"Перед запуском yolo-runner нужно уточнить детали.",
		russianNeedsInfoProxyNotice,
	}

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
	return b.client.RemoveLabel(ctx, issueID, label)
}

func (b *StorageBackend) AddLabel(ctx context.Context, issueID string, label string) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	return b.client.AddLabel(ctx, issueID, label)
}

func (b *StorageBackend) CreateIssueComment(ctx context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error) {
	if b == nil || b.client == nil {
		return IssueComment{}, errors.New("startrek storage backend is not initialized")
	}
	return b.client.CreateIssueComment(ctx, issueID, opts)
}
