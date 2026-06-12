package startrek

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	implementCompletedCommentMarker = "implementation-completed"
	parentPRCreatedMetadataKey      = "parent_pr_created"
	parentPRURLMetadataKey          = "parent_pr_url"
	defaultImplementCompletedLabel  = "yolo-agent-completed"
	defaultImplementBlockedLabel    = "yolo-agent-blocked"
	defaultImplementFailedLabel     = "yolo-agent-failed"
)

type implementStatusSetter interface {
	SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error
}

type implementTaskReader interface {
	GetTask(ctx context.Context, taskID string) (*contracts.Task, error)
}

type ImplementWritebackRecord struct {
	IdempotencyKey string    `json:"idempotency_key"`
	ItemID         string    `json:"item_id"`
	IssueID        string    `json:"issue_id"`
	ParentIssueID  string    `json:"parent_issue_id"`
	Status         string    `json:"status"`
	Branch         string    `json:"branch"`
	CommitSHA      string    `json:"commit_sha"`
	PRURL          string    `json:"pr_url"`
	CommentID      string    `json:"comment_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FinalizeWritebackRecord struct {
	ParentIssueID          string    `json:"parent_issue_id"`
	FinalizeIdempotencyKey string    `json:"finalize_idempotency_key"`
	FinalizeItemID         string    `json:"finalize_item_id"`
	PRURL                  string    `json:"pr_url"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (s *Source) handleImplementResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	if s.Tracker == nil {
		return nil, errors.New("startrek implement writeback tracker is required")
	}
	if s.State == nil {
		return nil, errors.New("startrek source state store is required")
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return nil, errors.New("startrek implement item idempotency key is required")
	}

	payload, err := decodeImplementPayload(item)
	if err != nil {
		return nil, err
	}
	implementResult, err := decodeImplementResult(item, result)
	if err != nil {
		return nil, err
	}
	taskStatus, resultStatus, implementResult := implementResultTaskUpdate(implementResult)

	issueID := implementIssueID(item, payload)
	if issueID == "" {
		return nil, errors.New("startrek implement issue id is required")
	}

	if record, ok, err := s.State.GetImplementWriteback(ctx, item.IdempotencyKey); err != nil {
		return nil, err
	} else if ok {
		if record.Status == string(contracts.RunnerResultCompleted) {
			return s.finalizeFollowUpIfReady(ctx, item, payload, record)
		}
		return nil, nil
	}

	parentID := strings.TrimSpace(payload.PromptContext.ParentID)
	if splitRecord, ok, err := s.State.GetSplitSubtaskItemForImplement(ctx, item.ID, item.IdempotencyKey, issueID); err != nil {
		return nil, err
	} else if ok {
		parentID = splitRecord.ParentIssueID
	}

	commentID, err := s.applyImplementWriteback(ctx, issueID, payload, implementResult, taskStatus)
	if err != nil {
		return nil, err
	}
	if err := s.State.RecordImplementWriteback(ctx, ImplementWritebackRecord{
		IdempotencyKey: strings.TrimSpace(item.IdempotencyKey),
		ItemID:         strings.TrimSpace(item.ID),
		IssueID:        issueID,
		ParentIssueID:  parentID,
		Status:         string(resultStatus),
		Branch:         strings.TrimSpace(implementResult.Branch),
		CommitSHA:      strings.TrimSpace(implementResult.CommitSHA),
		PRURL:          strings.TrimSpace(implementResult.PRURL),
		CommentID:      commentID,
	}); err != nil {
		return nil, err
	}

	if resultStatus != contracts.RunnerResultCompleted {
		return nil, nil
	}
	return s.finalizeFollowUpIfReady(ctx, item, payload, ImplementWritebackRecord{
		IdempotencyKey: strings.TrimSpace(item.IdempotencyKey),
		ItemID:         strings.TrimSpace(item.ID),
		IssueID:        issueID,
		ParentIssueID:  parentID,
		Status:         string(resultStatus),
		Branch:         strings.TrimSpace(implementResult.Branch),
		CommitSHA:      strings.TrimSpace(implementResult.CommitSHA),
		PRURL:          strings.TrimSpace(implementResult.PRURL),
		CommentID:      commentID,
	})
}

func (s *Source) handleFinalizeResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	if s.Tracker == nil {
		return nil, errors.New("startrek finalize writeback tracker is required")
	}
	if s.State == nil {
		return nil, errors.New("startrek source state store is required")
	}
	if result.Status != "" && result.Status != workqueue.ResultStatusCompleted {
		return nil, fmt.Errorf("startrek finalize result for item %q has unsupported status %q", item.ID, result.Status)
	}

	payload, err := decodeFinalizePayload(item)
	if err != nil {
		return nil, err
	}
	finalizeResult, err := decodeFinalizeResult(item, result)
	if err != nil {
		return nil, err
	}

	parentID := strings.TrimSpace(item.SourceRef)
	if parentID == "" {
		parentID = strings.TrimSpace(payload.ParentRef)
	}
	if parentID == "" {
		return nil, errors.New("startrek finalize parent issue id is required")
	}
	if existing, ok, err := s.State.GetFinalizeWriteback(ctx, parentID); err != nil {
		return nil, err
	} else if ok && strings.TrimSpace(existing.PRURL) != "" {
		return nil, nil
	}

	prURL := strings.TrimSpace(finalizeResult.PRURL)
	if prURL == "" {
		return nil, errors.New("startrek finalize result PR URL is required")
	}
	subtasks, err := s.State.ListSplitSubtaskItems(ctx, parentID)
	if err != nil {
		return nil, err
	}
	subtaskIDs := make([]string, 0, len(subtasks))
	for _, subtask := range subtasks {
		if subtaskID := strings.TrimSpace(subtask.SubtaskIssueID); subtaskID != "" {
			subtaskIDs = append(subtaskIDs, subtaskID)
		}
	}
	if len(subtaskIDs) == 0 {
		return nil, fmt.Errorf("startrek finalize parent %q has no split subtasks", parentID)
	}

	if err := s.Tracker.SetTaskData(ctx, parentID, map[string]string{
		parentPRCreatedMetadataKey: "true",
		parentPRURLMetadataKey:     prURL,
	}); err != nil {
		return nil, fmt.Errorf("record startrek parent PR metadata on issue %q: %w", parentID, err)
	}
	if err := trackerstartrek.PostParentPRCreatedComment(ctx, s.Tracker, parentID, prURL, subtaskIDs); err != nil {
		return nil, err
	}
	if err := s.State.RecordFinalizeCompleted(ctx, FinalizeWritebackRecord{
		ParentIssueID:          parentID,
		FinalizeItemID:         item.ID,
		FinalizeIdempotencyKey: item.IdempotencyKey,
		PRURL:                  prURL,
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Source) applyImplementWriteback(ctx context.Context, issueID string, payload workitem.ImplementPayload, result workitem.ImplementResult, status contracts.TaskStatus) (string, error) {
	switch status {
	case contracts.TaskStatusClosed:
		if err := s.setImplementTaskStatus(ctx, issueID, contracts.TaskStatusClosed); err != nil {
			return "", err
		}
		comment, err := s.postImplementCompletedComment(ctx, issueID, result)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(comment.ID), nil
	case contracts.TaskStatusBlocked:
		if err := s.setImplementTaskStatus(ctx, issueID, contracts.TaskStatusBlocked); err != nil {
			return "", err
		}
		if err := s.Tracker.SetTaskData(ctx, issueID, implementTerminalTaskData("blocked", result)); err != nil {
			return "", fmt.Errorf("record startrek blocked implement metadata on issue %q: %w", issueID, err)
		}
		comment, err := s.applyImplementNeedsInfo(ctx, issueID, payload, result)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(comment.ID), nil
	case contracts.TaskStatusFailed:
		if err := s.Tracker.SetTaskData(ctx, issueID, implementTerminalTaskData("failed", result)); err != nil {
			return "", fmt.Errorf("record startrek failed implement metadata on issue %q: %w", issueID, err)
		}
		if err := s.setImplementTaskStatus(ctx, issueID, contracts.TaskStatusFailed); err != nil {
			return "", err
		}
		if err := trackerstartrek.PostFailureComment(ctx, s.Tracker, issueID, implementResultReason(result)); err != nil {
			return "", err
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported startrek implement task status %q", status)
	}
}

func (s *Source) setImplementTaskStatus(ctx context.Context, issueID string, status contracts.TaskStatus) error {
	if setter, ok := s.Tracker.(implementStatusSetter); ok {
		return setter.SetTaskStatus(ctx, issueID, status)
	}
	addLabel, removeLabels, err := s.implementStatusLabelTransition(status)
	if err != nil {
		return err
	}
	for _, label := range removeLabels {
		if err := s.Tracker.RemoveLabel(ctx, issueID, label); err != nil {
			return fmt.Errorf("remove startrek status label %q from issue %q: %w", label, issueID, err)
		}
	}
	if addLabel != "" {
		if err := s.Tracker.AddLabel(ctx, issueID, addLabel); err != nil {
			return fmt.Errorf("add startrek status label %q to issue %q: %w", addLabel, issueID, err)
		}
	}
	return nil
}

func (s *Source) implementStatusLabelTransition(status contracts.TaskStatus) (string, []string, error) {
	labels := []string{
		s.readyLabel(),
		s.processingLabel(),
		defaultImplementCompletedLabel,
		defaultImplementBlockedLabel,
		defaultImplementFailedLabel,
	}
	switch status {
	case contracts.TaskStatusClosed:
		return defaultImplementCompletedLabel, implementLabelsExcept(labels, defaultImplementCompletedLabel), nil
	case contracts.TaskStatusBlocked:
		return defaultImplementBlockedLabel, implementLabelsExcept(labels, defaultImplementBlockedLabel), nil
	case contracts.TaskStatusFailed:
		return defaultImplementFailedLabel, implementLabelsExcept(labels, defaultImplementFailedLabel), nil
	default:
		return "", nil, fmt.Errorf("unsupported startrek implement task status %q", status)
	}
}

func (s *Source) applyImplementNeedsInfo(ctx context.Context, issueID string, payload workitem.ImplementPayload, result workitem.ImplementResult) (trackerstartrek.IssueComment, error) {
	reason := implementResultReason(result)
	task := contracts.Task{
		ID:          strings.TrimSpace(payload.TaskID),
		Title:       strings.TrimSpace(payload.Title),
		Description: payload.Description,
	}
	res, err := (trackerstartrek.NeedsInfoTransitionService{
		Tracker:         s.Tracker,
		ProcessingLabel: s.processingLabel(),
		NeedsInfoLabel:  s.needsInfoLabel(),
		Marker:          s.marker(),
	}).Apply(ctx, trackerstartrek.NeedsInfoTransitionInput{
		IssueID:    issueID,
		Summary:    reason,
		Questions:  []string{implementBlockedQuestion(reason)},
		SummoneeID: SummoneeIDFromTask(task),
	})
	if err != nil {
		return trackerstartrek.IssueComment{}, err
	}
	return res.Comment, nil
}

func (s *Source) postImplementCompletedComment(ctx context.Context, issueID string, result workitem.ImplementResult) (trackerstartrek.IssueComment, error) {
	body := implementCompletedCommentBody(result)
	comment, err := s.Tracker.CreateIssueComment(ctx, issueID, trackerstartrek.IssueCommentCreateOptions{
		Body:   body,
		Marker: implementCompletedCommentMarker,
	})
	if err != nil {
		return trackerstartrek.IssueComment{}, fmt.Errorf("post startrek implementation-completed comment on issue %q: %w", issueID, err)
	}
	return comment, nil
}

func (s *Source) finalizeFollowUpIfReady(ctx context.Context, item workitem.Item, payload workitem.ImplementPayload, record ImplementWritebackRecord) ([]workqueue.Submission, error) {
	issueID := implementIssueID(item, payload)
	splitRecord, ok, err := s.State.GetSplitSubtaskItemForImplement(ctx, item.ID, item.IdempotencyKey, issueID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	parentID := strings.TrimSpace(splitRecord.ParentIssueID)
	if parentID == "" {
		parentID = strings.TrimSpace(record.ParentIssueID)
	}
	if parentID == "" {
		return nil, nil
	}
	if _, ok, err := s.State.GetFinalizeWriteback(ctx, parentID); err != nil {
		return nil, err
	} else if ok {
		return nil, nil
	}

	subtasks, err := s.State.ListSplitSubtaskItems(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if len(subtasks) == 0 {
		return nil, nil
	}
	branches := make([]string, 0, len(subtasks))
	for _, subtask := range subtasks {
		writeback, ok, err := s.State.GetImplementWriteback(ctx, subtask.ImplementIdempotencyKey)
		if err != nil {
			return nil, err
		}
		if !ok || writeback.Status != string(contracts.RunnerResultCompleted) {
			return nil, nil
		}
		if branch := strings.TrimSpace(writeback.Branch); branch != "" {
			branches = append(branches, branch)
		}
	}

	key, err := implementFinalizeIdempotencyKey(item.IdempotencyKey, parentID)
	if err != nil {
		return nil, err
	}
	rawPayload, err := json.Marshal(workitem.FinalizePayload{
		ParentRef:     parentID,
		ChildBranches: branches,
		Title:         s.finalizeTitle(ctx, parentID),
	})
	if err != nil {
		return nil, fmt.Errorf("encode startrek finalize follow-up for parent %q: %w", parentID, err)
	}
	submission := workqueue.Submission{
		Kind:           workitem.KindFinalize,
		Source:         s.Name(),
		SourceRef:      parentID,
		IdempotencyKey: key,
		Preset:         strings.TrimSpace(item.Preset),
		Priority:       item.Priority,
		Payload:        rawPayload,
		MaxAttempts:    item.MaxAttempts,
	}
	if err := s.State.RecordFinalizeSubmission(ctx, FinalizeWritebackRecord{
		ParentIssueID:          parentID,
		FinalizeIdempotencyKey: key,
	}); err != nil {
		return nil, err
	}
	return []workqueue.Submission{submission}, nil
}

func (s *Source) finalizeTitle(ctx context.Context, parentID string) string {
	if reader, ok := s.Tracker.(implementTaskReader); ok {
		if task, err := reader.GetTask(ctx, parentID); err == nil && task != nil {
			if title := strings.TrimSpace(task.Title); title != "" {
				return title
			}
		}
	}
	if parentID = strings.TrimSpace(parentID); parentID != "" {
		return parentID
	}
	return "Complete split task set"
}

func decodeImplementPayload(item workitem.Item) (workitem.ImplementPayload, error) {
	raw := item.Payload
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	payload, err := workitem.DecodeImplementPayload(raw)
	if err != nil {
		return workitem.ImplementPayload{}, fmt.Errorf("decode startrek implement item payload %q: %w", item.ID, err)
	}
	return payload, nil
}

func decodeImplementResult(item workitem.Item, result workqueue.Result) (workitem.ImplementResult, error) {
	raw := result.Payload
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoded, err := workitem.DecodeImplementResult(raw)
	if err != nil {
		return workitem.ImplementResult{}, fmt.Errorf("decode startrek implement result for item %q: %w", item.ID, err)
	}
	if strings.TrimSpace(decoded.Status) == "" {
		switch result.Status {
		case workqueue.ResultStatusBlocked:
			decoded.Status = string(contracts.RunnerResultBlocked)
		case workqueue.ResultStatusFailed:
			decoded.Status = string(contracts.RunnerResultFailed)
		default:
			decoded.Status = string(contracts.RunnerResultCompleted)
		}
	}
	return decoded, nil
}

func decodeFinalizePayload(item workitem.Item) (workitem.FinalizePayload, error) {
	raw := item.Payload
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	payload, err := workitem.DecodeFinalizePayload(raw)
	if err != nil {
		return workitem.FinalizePayload{}, fmt.Errorf("decode startrek finalize item payload %q: %w", item.ID, err)
	}
	return payload, nil
}

func decodeFinalizeResult(item workitem.Item, result workqueue.Result) (workitem.FinalizeResult, error) {
	raw := result.Payload
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoded, err := workitem.DecodeFinalizeResult(raw)
	if err != nil {
		return workitem.FinalizeResult{}, fmt.Errorf("decode startrek finalize result for item %q: %w", item.ID, err)
	}
	return decoded, nil
}

func implementIssueID(item workitem.Item, payload workitem.ImplementPayload) string {
	if sourceRef := strings.TrimSpace(item.SourceRef); sourceRef != "" {
		return sourceRef
	}
	return strings.TrimSpace(payload.TaskID)
}

func implementResultTaskUpdate(result workitem.ImplementResult) (contracts.TaskStatus, contracts.RunnerResultStatus, workitem.ImplementResult) {
	switch contracts.RunnerResultStatus(strings.TrimSpace(result.Status)) {
	case contracts.RunnerResultCompleted:
		return contracts.TaskStatusClosed, contracts.RunnerResultCompleted, result
	case contracts.RunnerResultBlocked:
		return contracts.TaskStatusBlocked, contracts.RunnerResultBlocked, result
	case contracts.RunnerResultFailed:
		return contracts.TaskStatusFailed, contracts.RunnerResultFailed, result
	default:
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = fmt.Sprintf("invalid implement result status %q", result.Status)
		}
		result.Status = string(contracts.RunnerResultFailed)
		result.Reason = reason
		return contracts.TaskStatusFailed, contracts.RunnerResultFailed, result
	}
}

func implementTerminalTaskData(status string, result workitem.ImplementResult) map[string]string {
	status = strings.TrimSpace(status)
	if status == "" {
		return nil
	}
	data := map[string]string{
		"triage_status": status,
		"decision":      status,
	}
	if reason := implementResultReason(result); reason != "" {
		data["triage_reason"] = reason
		data["reason"] = reason
	}
	if branch := strings.TrimSpace(result.Branch); branch != "" {
		data["branch"] = branch
	}
	if commit := strings.TrimSpace(result.CommitSHA); commit != "" {
		data["commit_sha"] = commit
	}
	if prURL := strings.TrimSpace(result.PRURL); prURL != "" {
		data["pr_url"] = prURL
	}
	if verdict := strings.TrimSpace(result.ReviewVerdict); verdict != "" {
		data["review_verdict"] = verdict
	}
	return data
}

func implementResultReason(result workitem.ImplementResult) string {
	return firstNonEmptyStartrekString(
		result.Reason,
		result.Artifacts["triage_reason"],
		result.Artifacts["reason"],
	)
}

func implementBlockedQuestion(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "Implementation is blocked and needs human input before yolo-runner can continue."
	}
	return reason
}

func implementCompletedCommentBody(result workitem.ImplementResult) string {
	lines := []string{"### Implementation completed"}
	if branch := strings.TrimSpace(result.Branch); branch != "" {
		lines = append(lines, "", "Branch: "+branch)
	}
	if commit := strings.TrimSpace(result.CommitSHA); commit != "" {
		lines = append(lines, "Commit: "+commit)
	}
	if prURL := strings.TrimSpace(result.PRURL); prURL != "" {
		lines = append(lines, "PR: "+prURL)
	}
	if len(lines) == 1 {
		lines = append(lines, "", "The implementation completed successfully.")
	}
	return strings.Join(lines, "\n")
}

func implementFinalizeIdempotencyKey(implementKey string, parentID string) (string, error) {
	implementKey = strings.TrimSpace(implementKey)
	parentID = strings.TrimSpace(parentID)
	parts := strings.SplitN(implementKey, "/", 4)
	if len(parts) != 4 || parts[0] != "st" || parts[2] != string(workitem.KindImplement) || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("startrek implement idempotency key %q must match st/<issue>/implement/<rev>", implementKey)
	}
	if parentID == "" {
		return "", errors.New("startrek finalize idempotency key requires parent issue id")
	}
	return "st/" + parentID + "/" + string(workitem.KindFinalize) + "/" + strings.TrimSpace(parts[3]), nil
}

func implementLabelsExcept(labels []string, keep string) []string {
	keep = strings.TrimSpace(keep)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" || label == keep {
			continue
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, label)
	}
	return out
}

func firstNonEmptyStartrekString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *StateStore) ensureImplementWritebackTables(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return errors.New("startrek source state store is not initialized")
	}
	const schema = `
CREATE TABLE IF NOT EXISTS implement_writebacks (
	idempotency_key TEXT PRIMARY KEY,
	item_id TEXT NOT NULL DEFAULT '',
	issue_id TEXT NOT NULL,
	parent_issue_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	branch TEXT NOT NULL DEFAULT '',
	commit_sha TEXT NOT NULL DEFAULT '',
	pr_url TEXT NOT NULL DEFAULT '',
	comment_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS finalize_writebacks (
	parent_issue_id TEXT PRIMARY KEY,
	finalize_idempotency_key TEXT NOT NULL DEFAULT '',
	finalize_item_id TEXT NOT NULL DEFAULT '',
	pr_url TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize startrek implement writeback schema: %w", err)
	}
	return nil
}

func (s *StateStore) RecordImplementWriteback(ctx context.Context, record ImplementWritebackRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackTables(ctx); err != nil {
		return err
	}
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	record.ItemID = strings.TrimSpace(record.ItemID)
	record.IssueID = strings.TrimSpace(record.IssueID)
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.Status = strings.TrimSpace(record.Status)
	record.Branch = strings.TrimSpace(record.Branch)
	record.CommitSHA = strings.TrimSpace(record.CommitSHA)
	record.PRURL = strings.TrimSpace(record.PRURL)
	record.CommentID = strings.TrimSpace(record.CommentID)
	if record.IdempotencyKey == "" {
		return errors.New("startrek implement writeback idempotency key is required")
	}
	if record.IssueID == "" {
		return errors.New("startrek implement writeback issue id is required")
	}
	if record.Status == "" {
		return errors.New("startrek implement writeback status is required")
	}

	now := time.Now().UTC()
	formattedNow := formatSourceStateTime(now)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO implement_writebacks (
	idempotency_key, item_id, issue_id, parent_issue_id, status, branch,
	commit_sha, pr_url, comment_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(idempotency_key) DO UPDATE SET
	updated_at = excluded.updated_at`,
		record.IdempotencyKey,
		record.ItemID,
		record.IssueID,
		record.ParentIssueID,
		record.Status,
		record.Branch,
		record.CommitSHA,
		record.PRURL,
		record.CommentID,
		formattedNow,
		formattedNow,
	); err != nil {
		return fmt.Errorf("record startrek implement writeback for idempotency key %q: %w", record.IdempotencyKey, err)
	}
	return nil
}

func (s *StateStore) GetImplementWriteback(ctx context.Context, idempotencyKey string) (ImplementWritebackRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackTables(ctx); err != nil {
		return ImplementWritebackRecord{}, false, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return ImplementWritebackRecord{}, false, errors.New("startrek implement writeback idempotency key is required")
	}
	return s.scanImplementWriteback(ctx, `
SELECT idempotency_key, item_id, issue_id, parent_issue_id, status, branch,
	commit_sha, pr_url, comment_id, created_at, updated_at
FROM implement_writebacks
WHERE idempotency_key = ?`, idempotencyKey)
}

func (s *StateStore) GetSplitSubtaskItemForImplement(ctx context.Context, implementItemID string, implementIdempotencyKey string, subtaskIssueID string) (SplitSubtaskItemRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return SplitSubtaskItemRecord{}, false, errors.New("startrek source state store is not initialized")
	}
	implementItemID = emptyQuerySentinel(implementItemID)
	implementIdempotencyKey = emptyQuerySentinel(implementIdempotencyKey)
	subtaskIssueID = emptyQuerySentinel(subtaskIssueID)
	var record SplitSubtaskItemRecord
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT parent_issue_id, split_task_id, subtask_issue_id, implement_item_id,
	implement_idempotency_key, split_item_id, created_at, updated_at
FROM split_subtask_items
WHERE implement_item_id = ? OR implement_idempotency_key = ? OR subtask_issue_id = ?
ORDER BY created_at ASC, rowid ASC
LIMIT 1`,
		implementItemID,
		implementIdempotencyKey,
		subtaskIssueID,
	).Scan(
		&record.ParentIssueID,
		&record.SplitTaskID,
		&record.SubtaskIssueID,
		&record.ImplementItemID,
		&record.ImplementIdempotencyKey,
		&record.SplitItemID,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return SplitSubtaskItemRecord{}, false, nil
	}
	if err != nil {
		return SplitSubtaskItemRecord{}, false, fmt.Errorf("get startrek split subtask item for implement item %q: %w", implementItemID, err)
	}
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}

func (s *StateStore) ListSplitSubtaskItems(ctx context.Context, parentIssueID string) ([]SplitSubtaskItemRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return nil, errors.New("startrek source state store is not initialized")
	}
	parentIssueID = strings.TrimSpace(parentIssueID)
	if parentIssueID == "" {
		return nil, errors.New("startrek split subtask item parent issue id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT parent_issue_id, split_task_id, subtask_issue_id, implement_item_id,
	implement_idempotency_key, split_item_id, created_at, updated_at
FROM split_subtask_items
WHERE parent_issue_id = ?
ORDER BY created_at ASC, rowid ASC`, parentIssueID)
	if err != nil {
		return nil, fmt.Errorf("list startrek split subtask items for parent %q: %w", parentIssueID, err)
	}
	defer rows.Close()

	var records []SplitSubtaskItemRecord
	for rows.Next() {
		var record SplitSubtaskItemRecord
		var createdAt string
		var updatedAt string
		if err := rows.Scan(
			&record.ParentIssueID,
			&record.SplitTaskID,
			&record.SubtaskIssueID,
			&record.ImplementItemID,
			&record.ImplementIdempotencyKey,
			&record.SplitItemID,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan startrek split subtask item for parent %q: %w", parentIssueID, err)
		}
		record.CreatedAt = parseSourceStateTime(createdAt)
		record.UpdatedAt = parseSourceStateTime(updatedAt)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read startrek split subtask items for parent %q: %w", parentIssueID, err)
	}
	return records, nil
}

func (s *StateStore) RecordFinalizeSubmission(ctx context.Context, record FinalizeWritebackRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackTables(ctx); err != nil {
		return err
	}
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.FinalizeIdempotencyKey = strings.TrimSpace(record.FinalizeIdempotencyKey)
	if record.ParentIssueID == "" {
		return errors.New("startrek finalize writeback parent issue id is required")
	}
	if record.FinalizeIdempotencyKey == "" {
		return errors.New("startrek finalize writeback idempotency key is required")
	}

	now := time.Now().UTC()
	formattedNow := formatSourceStateTime(now)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO finalize_writebacks (
	parent_issue_id, finalize_idempotency_key, finalize_item_id, pr_url, created_at, updated_at
) VALUES (?, ?, '', '', ?, ?)
ON CONFLICT(parent_issue_id) DO UPDATE SET
	updated_at = excluded.updated_at`,
		record.ParentIssueID,
		record.FinalizeIdempotencyKey,
		formattedNow,
		formattedNow,
	); err != nil {
		return fmt.Errorf("record startrek finalize submission for parent %q: %w", record.ParentIssueID, err)
	}
	return nil
}

func (s *StateStore) RecordFinalizeCompleted(ctx context.Context, record FinalizeWritebackRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackTables(ctx); err != nil {
		return err
	}
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.FinalizeIdempotencyKey = strings.TrimSpace(record.FinalizeIdempotencyKey)
	record.FinalizeItemID = strings.TrimSpace(record.FinalizeItemID)
	record.PRURL = strings.TrimSpace(record.PRURL)
	if record.ParentIssueID == "" {
		return errors.New("startrek finalize writeback parent issue id is required")
	}
	if record.PRURL == "" {
		return errors.New("startrek finalize writeback PR URL is required")
	}

	now := time.Now().UTC()
	formattedNow := formatSourceStateTime(now)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO finalize_writebacks (
	parent_issue_id, finalize_idempotency_key, finalize_item_id, pr_url, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(parent_issue_id) DO UPDATE SET
	finalize_idempotency_key = excluded.finalize_idempotency_key,
	finalize_item_id = excluded.finalize_item_id,
	pr_url = excluded.pr_url,
	updated_at = excluded.updated_at`,
		record.ParentIssueID,
		record.FinalizeIdempotencyKey,
		record.FinalizeItemID,
		record.PRURL,
		formattedNow,
		formattedNow,
	); err != nil {
		return fmt.Errorf("record startrek finalize completion for parent %q: %w", record.ParentIssueID, err)
	}
	return nil
}

func (s *StateStore) GetFinalizeWriteback(ctx context.Context, parentIssueID string) (FinalizeWritebackRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackTables(ctx); err != nil {
		return FinalizeWritebackRecord{}, false, err
	}
	parentIssueID = strings.TrimSpace(parentIssueID)
	if parentIssueID == "" {
		return FinalizeWritebackRecord{}, false, errors.New("startrek finalize writeback parent issue id is required")
	}
	var record FinalizeWritebackRecord
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT parent_issue_id, finalize_idempotency_key, finalize_item_id, pr_url, created_at, updated_at
FROM finalize_writebacks
WHERE parent_issue_id = ?`, parentIssueID).Scan(
		&record.ParentIssueID,
		&record.FinalizeIdempotencyKey,
		&record.FinalizeItemID,
		&record.PRURL,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return FinalizeWritebackRecord{}, false, nil
	}
	if err != nil {
		return FinalizeWritebackRecord{}, false, fmt.Errorf("get startrek finalize writeback for parent %q: %w", parentIssueID, err)
	}
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}

func (s *StateStore) scanImplementWriteback(ctx context.Context, query string, args ...any) (ImplementWritebackRecord, bool, error) {
	var record ImplementWritebackRecord
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&record.IdempotencyKey,
		&record.ItemID,
		&record.IssueID,
		&record.ParentIssueID,
		&record.Status,
		&record.Branch,
		&record.CommitSHA,
		&record.PRURL,
		&record.CommentID,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return ImplementWritebackRecord{}, false, nil
	}
	if err != nil {
		return ImplementWritebackRecord{}, false, fmt.Errorf("scan startrek implement writeback: %w", err)
	}
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}

func emptyQuerySentinel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "\x00"
	}
	return value
}
