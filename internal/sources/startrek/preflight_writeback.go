package startrek

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"

	_ "modernc.org/sqlite"
)

const (
	defaultSourceName            = "startrek"
	defaultReadyLabel            = "yolo-agent-ready"
	defaultPreflightProcessLabel = "yolo-agent-in-progress"
	defaultNeedsInfoLabel        = "needs-info"
	defaultNeedsInfoMarker       = "needs-info"
)

type PreflightWritebackTracker interface {
	GetIssueComments(ctx context.Context, issueID string) ([]trackerstartrek.IssueComment, error)
	RemoveLabel(ctx context.Context, issueID string, label string) error
	AddLabel(ctx context.Context, issueID string, label string) error
	CreateIssueComment(ctx context.Context, issueID string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error)
	SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}

type PreflightReplyTracker interface {
	RemoveLabel(ctx context.Context, issueID string, label string) error
	AddLabel(ctx context.Context, issueID string, label string) error
	CreateIssueComment(ctx context.Context, issueID string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error)
}

type Source struct {
	SourceName      string
	Tracker         PreflightWritebackTracker
	State           *StateStore
	ReadyLabel      string
	ProcessingLabel string
	NeedsInfoLabel  string
	Marker          string
}

func (s *Source) Name() string {
	return fallbackSourceText(s.SourceName, defaultSourceName)
}

func (s *Source) Poll(context.Context) ([]workqueue.Submission, error) {
	return nil, nil
}

func (s *Source) HandleResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if item.Kind != workitem.KindPreflight {
		return nil, nil
	}
	return s.handlePreflightResult(ctx, item, result)
}

func (s *Source) handlePreflightResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	if s.Tracker == nil {
		return nil, errors.New("startrek preflight writeback tracker is required")
	}
	if s.State == nil {
		return nil, errors.New("startrek source state store is required")
	}
	if result.Status != "" && result.Status != workqueue.ResultStatusCompleted {
		return nil, fmt.Errorf("startrek preflight result for item %q has unsupported status %q", item.ID, result.Status)
	}

	idempotencyKey := strings.TrimSpace(item.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, errors.New("startrek preflight item idempotency key is required")
	}

	payload, err := decodePreflightPayload(item)
	if err != nil {
		return nil, err
	}
	preflightResult, err := decodePreflightResult(item, result)
	if err != nil {
		return nil, err
	}

	issueID := preflightIssueID(item, payload)
	if issueID == "" {
		return nil, errors.New("startrek preflight issue id is required")
	}
	task := payload.Task.ToTask()
	summoneeID := SummoneeIDFromTask(task)

	var comment trackerstartrek.IssueComment
	switch preflightResult.Verdict {
	case workitem.PreflightVerdictNeedsInfo:
		if _, ok, err := s.State.GetPreflightWriteback(ctx, idempotencyKey); err != nil {
			return nil, err
		} else if ok {
			return nil, nil
		}
		questions := preflightResult.Questions
		if len(normalizedPreflightQuestions(questions)) == 0 {
			questions = FallbackPreflightQuestions(task, preflightResult)
		}
		res, err := (trackerstartrek.NeedsInfoTransitionService{
			Tracker:         s.Tracker,
			ProcessingLabel: s.processingLabel(),
			NeedsInfoLabel:  s.needsInfoLabel(),
			Marker:          s.marker(),
		}).Apply(ctx, trackerstartrek.NeedsInfoTransitionInput{
			IssueID:    issueID,
			Summary:    preflightResult.Summary,
			Questions:  questions,
			SummoneeID: summoneeID,
		})
		if err != nil {
			return nil, err
		}
		comment = res.Comment
	case workitem.PreflightVerdictReply:
		if _, ok, err := s.State.GetPreflightWriteback(ctx, idempotencyKey); err != nil {
			return nil, err
		} else if ok {
			return nil, nil
		}
		comment, err = ApplyPreflightReply(ctx, s.Tracker, PreflightReplyInput{
			IssueID:         issueID,
			ProcessingLabel: s.processingLabel(),
			NeedsInfoLabel:  s.needsInfoLabel(),
			Marker:          s.marker(),
			ReplyText:       preflightResult.ReplyText,
			SummoneeID:      summoneeID,
		})
		if err != nil {
			return nil, err
		}
	case workitem.PreflightVerdictReady:
		if err := s.applyReadyTransition(ctx, issueID); err != nil {
			return nil, err
		}
		followUp, ok, err := s.readyFollowUpSubmission(item, payload, task, issueID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return []workqueue.Submission{followUp}, nil
	default:
		return nil, nil
	}

	if err := s.State.RecordPreflightWriteback(ctx, PreflightWritebackRecord{
		IdempotencyKey: idempotencyKey,
		ItemID:         item.ID,
		IssueID:        issueID,
		Verdict:        preflightResult.Verdict,
		CommentID:      strings.TrimSpace(comment.ID),
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Source) readyLabel() string {
	return fallbackSourceText(s.ReadyLabel, defaultReadyLabel)
}

func (s *Source) processingLabel() string {
	return fallbackSourceText(s.ProcessingLabel, defaultPreflightProcessLabel)
}

func (s *Source) needsInfoLabel() string {
	return fallbackSourceText(s.NeedsInfoLabel, defaultNeedsInfoLabel)
}

func (s *Source) marker() string {
	return fallbackSourceText(s.Marker, defaultNeedsInfoMarker)
}

func (s *Source) applyReadyTransition(ctx context.Context, issueID string) error {
	if err := s.Tracker.RemoveLabel(ctx, issueID, s.processingLabel()); err != nil {
		return fmt.Errorf("remove startrek processing label from ready issue %q: %w", issueID, err)
	}
	if err := s.Tracker.AddLabel(ctx, issueID, s.readyLabel()); err != nil {
		return fmt.Errorf("add startrek ready label to issue %q after preflight ready: %w", issueID, err)
	}
	return nil
}

func (s *Source) readyFollowUpSubmission(item workitem.Item, payload workitem.PreflightPayload, task contracts.Task, issueID string) (workqueue.Submission, bool, error) {
	action := PlanTrackerWatchStartrekTaskCycle(payload.QueueRoot.ToTask(), task, true)
	var kind workitem.Kind
	var submissionPayload json.RawMessage
	switch action {
	case TaskCycleSplit:
		kind = workitem.KindSplit
		raw, err := json.Marshal(workitem.SplitPayload{
			Task:      workitem.TaskPayloadFromTask(task),
			QueueRoot: payload.QueueRoot,
		})
		if err != nil {
			return workqueue.Submission{}, false, fmt.Errorf("encode startrek split follow-up for issue %q: %w", issueID, err)
		}
		submissionPayload = raw
	case TaskCycleImplement:
		kind = workitem.KindImplement
		raw, err := json.Marshal(workitem.ImplementPayload{
			TaskID:      strings.TrimSpace(task.ID),
			Title:       strings.TrimSpace(task.Title),
			Description: task.Description,
			PromptContext: workitem.ImplementPromptContext{
				ParentID: strings.TrimSpace(task.ParentID),
				Metadata: cloneStartrekStringMap(task.Metadata),
			},
		})
		if err != nil {
			return workqueue.Submission{}, false, fmt.Errorf("encode startrek implement follow-up for issue %q: %w", issueID, err)
		}
		submissionPayload = raw
	default:
		return workqueue.Submission{}, false, nil
	}

	key, err := preflightFollowUpIdempotencyKey(item.IdempotencyKey, issueID, string(kind))
	if err != nil {
		return workqueue.Submission{}, false, err
	}
	return workqueue.Submission{
		Kind:           kind,
		Source:         s.Name(),
		SourceRef:      issueID,
		IdempotencyKey: key,
		Preset:         strings.TrimSpace(item.Preset),
		Priority:       item.Priority,
		Payload:        submissionPayload,
		MaxAttempts:    item.MaxAttempts,
	}, true, nil
}

func preflightFollowUpIdempotencyKey(preflightKey string, issueID string, stage string) (string, error) {
	preflightKey = strings.TrimSpace(preflightKey)
	issueID = strings.TrimSpace(issueID)
	stage = strings.TrimSpace(stage)
	parts := strings.SplitN(preflightKey, "/", 4)
	if len(parts) != 4 || parts[0] != "st" || parts[2] != "preflight" || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("startrek preflight idempotency key %q must match st/<issue>/preflight/<rev>", preflightKey)
	}
	if issueID == "" {
		issueID = strings.TrimSpace(parts[1])
	}
	if issueID == "" || stage == "" {
		return "", fmt.Errorf("startrek follow-up idempotency key requires issue id and stage")
	}
	return "st/" + issueID + "/" + stage + "/" + strings.TrimSpace(parts[3]), nil
}

func cloneStartrekStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

type PreflightReplyInput struct {
	IssueID         string
	ProcessingLabel string
	NeedsInfoLabel  string
	Marker          string
	ReplyText       string
	SummoneeID      string
}

func ApplyPreflightReply(ctx context.Context, tracker PreflightReplyTracker, input PreflightReplyInput) (trackerstartrek.IssueComment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tracker == nil {
		return trackerstartrek.IssueComment{}, errors.New("startrek preflight reply backend is required")
	}
	issueID := strings.TrimSpace(input.IssueID)
	if issueID == "" {
		return trackerstartrek.IssueComment{}, errors.New("startrek preflight reply issue id is required")
	}
	replyText := strings.TrimSpace(input.ReplyText)
	if replyText == "" {
		return trackerstartrek.IssueComment{}, errors.New("startrek preflight reply text is required")
	}
	processingLabel := fallbackSourceText(input.ProcessingLabel, defaultPreflightProcessLabel)
	needsInfoLabel := fallbackSourceText(input.NeedsInfoLabel, defaultNeedsInfoLabel)
	marker := fallbackSourceText(input.Marker, defaultNeedsInfoMarker)

	if err := tracker.RemoveLabel(ctx, issueID, processingLabel); err != nil {
		return trackerstartrek.IssueComment{}, fmt.Errorf("remove startrek processing label from issue %q before preflight reply: %w", issueID, err)
	}
	if err := tracker.AddLabel(ctx, issueID, needsInfoLabel); err != nil {
		return trackerstartrek.IssueComment{}, fmt.Errorf("add startrek needs-info label to issue %q after preflight reply: %w", issueID, err)
	}
	comment, err := tracker.CreateIssueComment(ctx, issueID, trackerstartrek.IssueCommentCreateOptions{
		Body:     replyText,
		AuthorID: strings.TrimSpace(input.SummoneeID),
		Marker:   marker,
	})
	if err != nil {
		return trackerstartrek.IssueComment{}, fmt.Errorf("post startrek preflight reply on needs-info issue %q: %w", issueID, err)
	}
	return comment, nil
}

func FallbackPreflightQuestions(task contracts.Task, result workitem.PreflightResult) []string {
	summary := strings.TrimSpace(result.Summary)
	russian := taskLooksRussian(task)
	if summary != "" {
		if russian {
			return []string{fmt.Sprintf("Предварительная проверка не может продолжить: %s Уточните этот блокер.", summary)}
		}
		return []string{fmt.Sprintf("Preflight could not proceed because: %s Please clarify this blocker.", summary)}
	}
	taskTitle := strings.TrimSpace(task.Title)
	if taskTitle == "" {
		taskTitle = strings.TrimSpace(task.ID)
	}
	if taskTitle != "" {
		if russian {
			return []string{fmt.Sprintf("Предварительная проверка пометила задачу %q как требующую уточнений, но не сформулировала конкретный вопрос. Добавьте в задачу недостающие детали для реализации.", taskTitle)}
		}
		return []string{fmt.Sprintf("Preflight marked %q as needing clarification but did not provide a concrete question. Please update the task with the specific missing implementation details.", taskTitle)}
	}
	if russian {
		return []string{"Предварительная проверка пометила задачу как требующую уточнений, но не сформулировала конкретный вопрос. Добавьте в задачу недостающие детали для реализации."}
	}
	return []string{"Preflight marked this task as needing clarification but did not provide a concrete question. Please update the task with the specific missing implementation details."}
}

func SummoneeIDFromTask(task contracts.Task) string {
	for _, line := range strings.Split(task.Description, "\n") {
		line = strings.TrimSpace(line)
		value, ok := strings.CutPrefix(line, "Author:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if start := strings.LastIndex(value, "("); start >= 0 && strings.HasSuffix(value, ")") {
			return strings.TrimSpace(strings.TrimSuffix(value[start+1:], ")"))
		}
		return value
	}
	return ""
}

func decodePreflightPayload(item workitem.Item) (workitem.PreflightPayload, error) {
	var payload workitem.PreflightPayload
	if len(item.Payload) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return workitem.PreflightPayload{}, fmt.Errorf("decode startrek preflight item payload %q: %w", item.ID, err)
	}
	return payload, nil
}

func decodePreflightResult(item workitem.Item, result workqueue.Result) (workitem.PreflightResult, error) {
	var preflightResult workitem.PreflightResult
	if err := json.Unmarshal(result.Payload, &preflightResult); err != nil {
		return workitem.PreflightResult{}, fmt.Errorf("decode startrek preflight result for item %q: %w", item.ID, err)
	}
	return preflightResult, nil
}

func preflightIssueID(item workitem.Item, payload workitem.PreflightPayload) string {
	if sourceRef := strings.TrimSpace(item.SourceRef); sourceRef != "" {
		return sourceRef
	}
	return strings.TrimSpace(payload.Task.ID)
}

func normalizedPreflightQuestions(questions []string) []string {
	normalized := make([]string, 0, len(questions))
	for _, question := range questions {
		question = strings.TrimSpace(question)
		if question != "" {
			normalized = append(normalized, question)
		}
	}
	return normalized
}

func taskLooksRussian(task contracts.Task) bool {
	return containsCyrillic(task.Title) || containsCyrillic(task.Description)
}

func containsCyrillic(value string) bool {
	for _, r := range value {
		if r >= '\u0400' && r <= '\u04FF' {
			return true
		}
	}
	return false
}

func fallbackSourceText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

type StateStore struct {
	db *sql.DB
}

type PreflightWritebackRecord struct {
	IdempotencyKey string                    `json:"idempotency_key"`
	ItemID         string                    `json:"item_id"`
	IssueID        string                    `json:"issue_id"`
	Verdict        workitem.PreflightVerdict `json:"verdict"`
	CommentID      string                    `json:"comment_id"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
}

func OpenState(path string) (*StateStore, error) {
	if err := ensureSourceStateParentDir(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open startrek source state db: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &StateStore{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func ensureSourceStateParentDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create startrek source state directory %q: %w", dir, err)
	}
	return nil
}

func (s *StateStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *StateStore) init() error {
	if s == nil || s.db == nil {
		return errors.New("startrek source state store is not initialized")
	}
	const schema = `
CREATE TABLE IF NOT EXISTS preflight_writebacks (
	idempotency_key TEXT PRIMARY KEY,
	item_id TEXT NOT NULL DEFAULT '',
	issue_id TEXT NOT NULL,
	verdict TEXT NOT NULL,
	comment_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize startrek source state schema: %w", err)
	}
	return nil
}

func (s *StateStore) RecordPreflightWriteback(ctx context.Context, record PreflightWritebackRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return errors.New("startrek source state store is not initialized")
	}
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	if record.IdempotencyKey == "" {
		return errors.New("startrek preflight writeback idempotency key is required")
	}
	record.IssueID = strings.TrimSpace(record.IssueID)
	if record.IssueID == "" {
		return errors.New("startrek preflight writeback issue id is required")
	}
	record.ItemID = strings.TrimSpace(record.ItemID)
	record.CommentID = strings.TrimSpace(record.CommentID)
	record.Verdict = workitem.PreflightVerdict(strings.TrimSpace(string(record.Verdict)))
	if record.Verdict == "" {
		return errors.New("startrek preflight writeback verdict is required")
	}

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO preflight_writebacks (
	idempotency_key, item_id, issue_id, verdict, comment_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(idempotency_key) DO UPDATE SET
	updated_at = excluded.updated_at`,
		record.IdempotencyKey,
		record.ItemID,
		record.IssueID,
		string(record.Verdict),
		record.CommentID,
		formatSourceStateTime(now),
		formatSourceStateTime(now),
	); err != nil {
		return fmt.Errorf("record startrek preflight writeback for idempotency key %q: %w", record.IdempotencyKey, err)
	}
	return nil
}

func (s *StateStore) GetPreflightWriteback(ctx context.Context, idempotencyKey string) (PreflightWritebackRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return PreflightWritebackRecord{}, false, errors.New("startrek source state store is not initialized")
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return PreflightWritebackRecord{}, false, errors.New("startrek preflight writeback idempotency key is required")
	}

	var record PreflightWritebackRecord
	var verdict string
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT idempotency_key, item_id, issue_id, verdict, comment_id, created_at, updated_at
FROM preflight_writebacks
WHERE idempotency_key = ?`, idempotencyKey).Scan(
		&record.IdempotencyKey,
		&record.ItemID,
		&record.IssueID,
		&verdict,
		&record.CommentID,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return PreflightWritebackRecord{}, false, nil
	}
	if err != nil {
		return PreflightWritebackRecord{}, false, fmt.Errorf("get startrek preflight writeback for idempotency key %q: %w", idempotencyKey, err)
	}
	record.Verdict = workitem.PreflightVerdict(verdict)
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}

func formatSourceStateTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseSourceStateTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
