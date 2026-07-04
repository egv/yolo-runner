package startrek

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	implementationCompletedCommentMarker = "implementation-completed"
	parentSplitSubtaskIDsMetadataKey     = "split_subtask_ids"
	parentPRCreatedMetadataKey           = "parent_pr_created"
	parentPRURLMetadataKey               = "parent_pr_url"
)

type ImplementWritebackTracker interface {
	PreflightWritebackTracker
	GetTask(ctx context.Context, taskID string) (*contracts.Task, error)
	SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error
}

type implementProgressCommentTracker interface {
	CreateIssueComment(ctx context.Context, issueID string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error)
}

type ImplementWritebackRecord struct {
	IdempotencyKey string               `json:"idempotency_key"`
	ItemID         string               `json:"item_id"`
	IssueID        string               `json:"issue_id"`
	ParentIssueID  string               `json:"parent_issue_id"`
	Status         contracts.TaskStatus `json:"status"`
	Branch         string               `json:"branch"`
	CommitSHA      string               `json:"commit_sha"`
	PRURL          string               `json:"pr_url"`
	ReviewVerdict  string               `json:"review_verdict"`
	CommentID      string               `json:"comment_id"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

type FinalizeSubmissionRecord struct {
	ParentIssueID           string    `json:"parent_issue_id"`
	IdempotencyKey          string    `json:"idempotency_key"`
	ImplementItemID         string    `json:"implement_item_id"`
	ImplementIdempotencyKey string    `json:"implement_idempotency_key"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type FinalizeWritebackRecord struct {
	IdempotencyKey string    `json:"idempotency_key"`
	ItemID         string    `json:"item_id"`
	ParentIssueID  string    `json:"parent_issue_id"`
	PRURL          string    `json:"pr_url"`
	CommentID      string    `json:"comment_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (s *Source) handleImplementResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	tracker, ok := s.Tracker.(ImplementWritebackTracker)
	if !ok || tracker == nil {
		return nil, errors.New("startrek implement writeback tracker is required")
	}
	if s.State == nil {
		return nil, errors.New("startrek source state store is required")
	}

	idempotencyKey := strings.TrimSpace(item.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, errors.New("startrek implement item idempotency key is required")
	}
	if existing, ok, err := s.State.GetImplementWriteback(ctx, idempotencyKey); err != nil {
		return nil, err
	} else if ok {
		if existing.Status != contracts.TaskStatusClosed {
			return nil, nil
		}
		payload, err := decodeStartrekImplementPayload(item)
		if err != nil {
			return nil, err
		}
		implementResult, err := decodeStartrekImplementResult(item, result)
		if err != nil {
			return nil, err
		}
		implementResult = fillImplementResultFromRecord(implementResult, existing)
		return s.finalizeFollowUpIfSplitComplete(ctx, tracker, item, payload, implementResult, existing)
	}

	payload, err := decodeStartrekImplementPayload(item)
	if err != nil {
		return nil, err
	}
	implementResult, err := decodeStartrekImplementResult(item, result)
	if err != nil {
		return nil, err
	}
	taskID := implementIssueID(item, payload)
	if taskID == "" {
		return nil, errors.New("startrek implement issue id is required")
	}
	task, err := implementWritebackTask(ctx, tracker, taskID, payload)
	if err != nil {
		return nil, err
	}
	parentID := firstNonEmptyStartrekString(payload.PromptContext.ParentID, task.ParentID)
	if parentID == "" {
		if splitRecord, ok, err := s.State.GetSplitSubtaskItemBySubtask(ctx, taskID); err != nil {
			return nil, err
		} else if ok {
			parentID = splitRecord.ParentIssueID
		}
	}

	taskStatus, taskData := startrekTaskUpdateFromImplementResult(implementResult)
	if len(taskData) > 0 {
		if err := tracker.SetTaskData(ctx, taskID, taskData); err != nil {
			return nil, fmt.Errorf("write startrek implement result data for issue %q: %w", taskID, err)
		}
	}
	if err := tracker.SetTaskStatus(ctx, taskID, taskStatus); err != nil {
		return nil, fmt.Errorf("set startrek issue %q status to %q after implement result: %w", taskID, taskStatus, err)
	}

	var commentID string
	switch taskStatus {
	case contracts.TaskStatusClosed:
		comment, err := postImplementationCompletedComment(ctx, tracker, taskID, implementResult)
		if err != nil {
			return nil, err
		}
		commentID = strings.TrimSpace(comment.ID)
	case contracts.TaskStatusBlocked:
		res, err := (trackerstartrek.NeedsInfoTransitionService{
			Tracker:         tracker,
			ProcessingLabel: s.processingLabel(),
			Marker:          s.marker(),
		}).Apply(ctx, trackerstartrek.NeedsInfoTransitionInput{
			IssueID:    taskID,
			Summary:    implementBlockedSummary(task, implementResult),
			Questions:  implementBlockedQuestions(task, implementResult),
			SummoneeID: SummoneeIDFromTask(task),
		})
		if err != nil {
			return nil, err
		}
		commentID = strings.TrimSpace(res.Comment.ID)
	case contracts.TaskStatusFailed:
		if err := trackerstartrek.PostFailureComment(ctx, tracker, taskID, implementResult.Reason); err != nil {
			return nil, err
		}
	}

	record := ImplementWritebackRecord{
		IdempotencyKey: idempotencyKey,
		ItemID:         item.ID,
		IssueID:        taskID,
		ParentIssueID:  parentID,
		Status:         taskStatus,
		Branch:         implementResult.Branch,
		CommitSHA:      implementResult.CommitSHA,
		PRURL:          implementResult.PRURL,
		ReviewVerdict:  implementResult.ReviewVerdict,
		CommentID:      commentID,
	}
	if err := s.State.RecordImplementWriteback(ctx, record); err != nil {
		return nil, err
	}
	if taskStatus != contracts.TaskStatusClosed {
		return nil, nil
	}
	return s.finalizeFollowUpIfSplitComplete(ctx, tracker, item, payload, implementResult, record)
}

func (s *Source) handleFinalizeResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	tracker, ok := s.Tracker.(ImplementWritebackTracker)
	if !ok || tracker == nil {
		return nil, errors.New("startrek finalize writeback tracker is required")
	}
	if s.State == nil {
		return nil, errors.New("startrek source state store is required")
	}
	if result.Status != "" && result.Status != workqueue.ResultStatusCompleted {
		return nil, fmt.Errorf("startrek finalize result for item %q has unsupported status %q", item.ID, result.Status)
	}

	idempotencyKey := strings.TrimSpace(item.IdempotencyKey)
	if idempotencyKey == "" {
		return nil, errors.New("startrek finalize item idempotency key is required")
	}
	if _, ok, err := s.State.GetFinalizeWriteback(ctx, idempotencyKey); err != nil {
		return nil, err
	} else if ok {
		return nil, nil
	}

	payload, err := decodeStartrekFinalizePayload(item)
	if err != nil {
		return nil, err
	}
	finalizeResult, err := decodeStartrekFinalizeResult(item, result)
	if err != nil {
		return nil, err
	}
	parentID := firstNonEmptyStartrekString(item.SourceRef, payload.ParentRef)
	if parentID == "" {
		return nil, errors.New("startrek finalize parent issue id is required")
	}
	prURL := strings.TrimSpace(finalizeResult.PRURL)
	if prURL == "" {
		return nil, errors.New("startrek finalize result PR URL is required")
	}

	subtaskIDs, err := s.parentSplitSubtaskIDs(ctx, tracker, parentID)
	if err != nil {
		return nil, err
	}
	if len(subtaskIDs) == 0 {
		return nil, fmt.Errorf("startrek finalize parent %q has no split subtask ids", parentID)
	}

	parentData := map[string]string{
		parentPRCreatedMetadataKey: "true",
		parentPRURLMetadataKey:     prURL,
		"pr_url":                   prURL,
	}
	if err := tracker.SetTaskData(ctx, parentID, parentData); err != nil {
		return nil, fmt.Errorf("write startrek parent PR data for issue %q: %w", parentID, err)
	}
	if err := trackerstartrek.PostParentPRCreatedComment(ctx, tracker, parentID, prURL, subtaskIDs); err != nil {
		return nil, err
	}
	if err := s.State.RecordFinalizeWriteback(ctx, FinalizeWritebackRecord{
		IdempotencyKey: idempotencyKey,
		ItemID:         item.ID,
		ParentIssueID:  parentID,
		PRURL:          prURL,
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Source) finalizeFollowUpIfSplitComplete(ctx context.Context, tracker ImplementWritebackTracker, item workitem.Item, payload workitem.ImplementPayload, result workitem.ImplementResult, record ImplementWritebackRecord) ([]workqueue.Submission, error) {
	parentID := strings.TrimSpace(record.ParentIssueID)
	if parentID == "" {
		return nil, nil
	}
	subtasks, err := s.State.ListSplitSubtaskItems(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if len(subtasks) == 0 {
		return nil, nil
	}

	parent, err := tracker.GetTask(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("get startrek parent issue %q before finalize: %w", parentID, err)
	}
	if parent == nil {
		return nil, fmt.Errorf("startrek parent issue %q is missing before finalize", parentID)
	}
	if parentPRAlreadyCreatedStartrek(parent.Metadata) {
		return nil, nil
	}

	childBranches := make([]string, 0, len(subtasks))
	for _, subtask := range subtasks {
		childID := strings.TrimSpace(subtask.SubtaskIssueID)
		if childID == "" {
			continue
		}
		child, err := tracker.GetTask(ctx, childID)
		if err != nil {
			return nil, fmt.Errorf("get startrek split subtask %q before finalize: %w", childID, err)
		}
		if child == nil || child.Status != contracts.TaskStatusClosed {
			return nil, nil
		}
		branch := ""
		if childID == strings.TrimSpace(record.IssueID) {
			branch = strings.TrimSpace(result.Branch)
		}
		if branch == "" && child != nil {
			branch = strings.TrimSpace(child.Metadata["branch"])
		}
		if branch == "" {
			if writeback, ok, err := s.State.GetImplementWriteback(ctx, subtask.ImplementIdempotencyKey); err != nil {
				return nil, err
			} else if ok {
				branch = strings.TrimSpace(writeback.Branch)
			}
		}
		if branch != "" {
			childBranches = append(childBranches, branch)
		}
	}

	key, err := finalizeFollowUpIdempotencyKey(item.IdempotencyKey, parentID)
	if err != nil {
		return nil, err
	}
	finalizePayload, err := json.Marshal(workitem.FinalizePayload{
		ParentRef:     parentID,
		ChildBranches: childBranches,
		Title:         parentPRTitleStartrek(*parent),
	})
	if err != nil {
		return nil, fmt.Errorf("encode startrek finalize follow-up for parent %q: %w", parentID, err)
	}
	return []workqueue.Submission{{
		Kind:           workitem.KindFinalize,
		Source:         s.Name(),
		SourceRef:      parentID,
		IdempotencyKey: key,
		Preset:         strings.TrimSpace(item.Preset),
		Priority:       item.Priority,
		Payload:        finalizePayload,
		MaxAttempts:    item.MaxAttempts,
	}}, nil
}

func (s *Source) parentSplitSubtaskIDs(ctx context.Context, tracker ImplementWritebackTracker, parentID string) ([]string, error) {
	subtasks, err := s.State.ListSplitSubtaskItems(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if len(subtasks) > 0 {
		ids := make([]string, 0, len(subtasks))
		for _, subtask := range subtasks {
			if id := strings.TrimSpace(subtask.SubtaskIssueID); id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	parent, err := tracker.GetTask(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("get startrek parent issue %q split metadata: %w", parentID, err)
	}
	if parent == nil {
		return nil, nil
	}
	return splitSubtaskIDsFromStartrekMetadata(parent.Metadata), nil
}

func implementWritebackTask(ctx context.Context, tracker ImplementWritebackTracker, taskID string, payload workitem.ImplementPayload) (contracts.Task, error) {
	task, err := tracker.GetTask(ctx, taskID)
	if err != nil {
		return contracts.Task{}, fmt.Errorf("get startrek implement issue %q: %w", taskID, err)
	}
	if task != nil {
		if task.Metadata == nil {
			task.Metadata = map[string]string{}
		}
		return *task, nil
	}
	return contracts.Task{
		ID:          taskID,
		Title:       payload.Title,
		Description: payload.Description,
		ParentID:    payload.PromptContext.ParentID,
		Metadata:    cloneStartrekStringMap(payload.PromptContext.Metadata),
	}, nil
}

func decodeStartrekImplementPayload(item workitem.Item) (workitem.ImplementPayload, error) {
	raw := item.Payload
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	payload, err := workitem.DecodeImplementPayload(raw)
	if err != nil {
		return workitem.ImplementPayload{}, fmt.Errorf("decode startrek implement item payload %q: %w", item.ID, err)
	}
	return payload, nil
}

func decodeStartrekImplementResult(item workitem.Item, result workqueue.Result) (workitem.ImplementResult, error) {
	raw := result.Payload
	if len(strings.TrimSpace(string(raw))) == 0 {
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

func decodeStartrekFinalizePayload(item workitem.Item) (workitem.FinalizePayload, error) {
	raw := item.Payload
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	payload, err := workitem.DecodeFinalizePayload(raw)
	if err != nil {
		return workitem.FinalizePayload{}, fmt.Errorf("decode startrek finalize item payload %q: %w", item.ID, err)
	}
	return payload, nil
}

func decodeStartrekFinalizeResult(item workitem.Item, result workqueue.Result) (workitem.FinalizeResult, error) {
	raw := result.Payload
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoded, err := workitem.DecodeFinalizeResult(raw)
	if err != nil {
		return workitem.FinalizeResult{}, fmt.Errorf("decode startrek finalize result for item %q: %w", item.ID, err)
	}
	return decoded, nil
}

func fillImplementResultFromRecord(result workitem.ImplementResult, record ImplementWritebackRecord) workitem.ImplementResult {
	if strings.TrimSpace(result.Branch) == "" {
		result.Branch = record.Branch
	}
	if strings.TrimSpace(result.CommitSHA) == "" {
		result.CommitSHA = record.CommitSHA
	}
	if strings.TrimSpace(result.PRURL) == "" {
		result.PRURL = record.PRURL
	}
	if strings.TrimSpace(result.ReviewVerdict) == "" {
		result.ReviewVerdict = record.ReviewVerdict
	}
	return result
}

func implementIssueID(item workitem.Item, payload workitem.ImplementPayload) string {
	if sourceRef := strings.TrimSpace(item.SourceRef); sourceRef != "" {
		return sourceRef
	}
	return strings.TrimSpace(payload.TaskID)
}

func startrekTaskUpdateFromImplementResult(result workitem.ImplementResult) (contracts.TaskStatus, map[string]string) {
	switch contracts.RunnerResultStatus(strings.TrimSpace(result.Status)) {
	case contracts.RunnerResultCompleted:
		return contracts.TaskStatusClosed, nil
	case contracts.RunnerResultBlocked:
		return contracts.TaskStatusBlocked, startrekTerminalResultData("blocked", result)
	case contracts.RunnerResultFailed:
		return contracts.TaskStatusFailed, startrekTerminalResultData("failed", result)
	default:
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = fmt.Sprintf("invalid implement result status %q", result.Status)
		}
		result.Status = string(contracts.RunnerResultFailed)
		result.Reason = reason
		return contracts.TaskStatusFailed, startrekTerminalResultData("failed", result)
	}
}

func startrekTerminalResultData(triageStatus string, result workitem.ImplementResult) map[string]string {
	triageStatus = strings.TrimSpace(triageStatus)
	if triageStatus == "" {
		return nil
	}

	data := map[string]string{
		"triage_status": triageStatus,
		"decision":      triageStatus,
	}
	reason := firstNonEmptyStartrekString(result.Reason, result.Artifacts["triage_reason"], result.Artifacts["reason"])
	if reason != "" {
		data["triage_reason"] = reason
		data["reason"] = reason
	}
	appendStartrekResultReviewOutcome(data, result)
	switch triageStatus {
	case "blocked":
		copyStartrekResultArtifact(data, result, "completion_retry_count")
		copyStartrekResultArtifact(data, result, "completion_addendum")
		copyStartrekResultArtifact(data, result, "landing_status")
		copyStartrekResultArtifact(data, result, "auto_commit_sha")
	case "failed":
		copyStartrekResultArtifact(data, result, "review_retry_count")
	}
	return compactStartrekMetadata(data)
}

func appendStartrekResultReviewOutcome(data map[string]string, result workitem.ImplementResult) {
	verdict := strings.ToLower(firstNonEmptyStartrekString(result.ReviewVerdict, result.Artifacts["review_verdict"]))
	if verdict == "pass" || verdict == "fail" {
		data["review_verdict"] = verdict
	}
	if feedback := firstNonEmptyStartrekString(result.Artifacts["review_fail_feedback"], result.Artifacts["review_feedback"]); feedback != "" {
		data["review_fail_feedback"] = feedback
	}
}

func copyStartrekResultArtifact(data map[string]string, result workitem.ImplementResult, key string) {
	if value := strings.TrimSpace(result.Artifacts[key]); value != "" {
		data[key] = value
	}
}

func implementBlockedQuestions(task contracts.Task, result workitem.ImplementResult) []string {
	reason := firstNonEmptyStartrekString(result.Reason, result.Artifacts["triage_reason"], result.Artifacts["reason"])
	if startrekTaskLooksRussian(task) {
		if reason != "" {
			if implementBlockedReasonLooksTimeout(reason) {
				return []string{"Запуск yolo-runner остановился по таймауту до завершения. Нужно продолжить задачу вручную или перезапустить после исправления причины таймаута?"}
			}
			return []string{"Нужно уточнить блокер реализации: " + reason}
		}
		title := strings.TrimSpace(task.Title)
		if title != "" {
			return []string{fmt.Sprintf("Что блокирует реализацию задачи %q?", title)}
		}
		return []string{"Что блокирует реализацию задачи?"}
	}
	if reason != "" {
		return []string{reason}
	}
	title := strings.TrimSpace(task.Title)
	if title != "" {
		return []string{fmt.Sprintf("Please clarify the blocker for %q.", title)}
	}
	return []string{"Please clarify the implementation blocker."}
}

func implementBlockedSummary(task contracts.Task, result workitem.ImplementResult) string {
	reason := firstNonEmptyStartrekString(result.Reason, result.Artifacts["triage_reason"], result.Artifacts["reason"])
	if !startrekTaskLooksRussian(task) {
		return reason
	}
	if reason == "" {
		return "Реализация заблокирована и требует уточнения."
	}
	if implementBlockedReasonLooksTimeout(reason) {
		return "Запуск yolo-runner остановился по таймауту до завершения задачи."
	}
	return "Реализация заблокирована: " + reason
}

func implementBlockedReasonLooksTimeout(reason string) bool {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	return strings.Contains(normalized, "timeout") || strings.Contains(normalized, "timed out")
}

func startrekTaskLooksRussian(task contracts.Task) bool {
	for _, r := range task.Title + "\n" + task.Description {
		if unicode.In(r, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

func postImplementationCompletedComment(ctx context.Context, tracker implementProgressCommentTracker, issueID string, result workitem.ImplementResult) (trackerstartrek.IssueComment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tracker == nil {
		return trackerstartrek.IssueComment{}, errors.New("startrek comment tracker is required")
	}
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return trackerstartrek.IssueComment{}, errors.New("startrek issue id is required")
	}
	body := implementationCompletedCommentBody(result)
	comment, err := tracker.CreateIssueComment(ctx, issueID, trackerstartrek.IssueCommentCreateOptions{
		Body:   body,
		Marker: implementationCompletedCommentMarker,
	})
	if err != nil {
		return trackerstartrek.IssueComment{}, fmt.Errorf("post startrek implementation-completed comment on issue %q: %w", issueID, err)
	}
	return comment, nil
}

func implementationCompletedCommentBody(result workitem.ImplementResult) string {
	var b strings.Builder
	b.WriteString("### Implementation completed")
	writeImplementationCompletedField(&b, "Branch", result.Branch)
	writeImplementationCompletedField(&b, "Commit", result.CommitSHA)
	writeImplementationCompletedField(&b, "PR", result.PRURL)
	writeImplementationCompletedField(&b, "Review verdict", result.ReviewVerdict)
	if strings.TrimSpace(result.Branch) == "" && strings.TrimSpace(result.PRURL) == "" {
		b.WriteString("\n\nNo branch or PR was reported.")
	}
	return b.String()
}

func writeImplementationCompletedField(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString("\n\n")
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
}

func finalizeFollowUpIdempotencyKey(implementKey string, parentID string) (string, error) {
	implementKey = strings.TrimSpace(implementKey)
	parentID = strings.TrimSpace(parentID)
	parts := strings.SplitN(implementKey, "/", 4)
	if len(parts) != 4 || parts[0] != "st" || parts[2] != "implement" || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("startrek implement idempotency key %q must match st/<issue>/implement/<rev>", implementKey)
	}
	if parentID == "" {
		return "", errors.New("startrek finalize follow-up idempotency key requires parent issue id")
	}
	return "st/" + parentID + "/finalize/" + strings.TrimSpace(parts[3]), nil
}

func parentPRAlreadyCreatedStartrek(metadata map[string]string) bool {
	if strings.TrimSpace(metadata[parentPRURLMetadataKey]) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(metadata[parentPRCreatedMetadataKey]), "true")
}

func parentPRTitleStartrek(parent contracts.Task) string {
	title := strings.TrimSpace(parent.Title)
	if title != "" {
		return title
	}
	if id := strings.TrimSpace(parent.ID); id != "" {
		return id
	}
	return "Complete split task set"
}

func splitSubtaskIDsFromStartrekMetadata(metadata map[string]string) []string {
	raw := strings.TrimSpace(metadata[parentSplitSubtaskIDsMetadataKey])
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	ids := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func firstNonEmptyStartrekString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func compactStartrekMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	filtered := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func (s *StateStore) ensureImplementWritebackSchema(ctx context.Context) error {
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
	review_verdict TEXT NOT NULL DEFAULT '',
	comment_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS finalize_submissions (
	parent_issue_id TEXT PRIMARY KEY,
	idempotency_key TEXT NOT NULL,
	implement_item_id TEXT NOT NULL DEFAULT '',
	implement_idempotency_key TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS finalize_writebacks (
	idempotency_key TEXT PRIMARY KEY,
	item_id TEXT NOT NULL DEFAULT '',
	parent_issue_id TEXT NOT NULL,
	pr_url TEXT NOT NULL,
	comment_id TEXT NOT NULL DEFAULT '',
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
	if err := s.ensureImplementWritebackSchema(ctx); err != nil {
		return err
	}
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	record.ItemID = strings.TrimSpace(record.ItemID)
	record.IssueID = strings.TrimSpace(record.IssueID)
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.Status = contracts.TaskStatus(strings.TrimSpace(string(record.Status)))
	record.Branch = strings.TrimSpace(record.Branch)
	record.CommitSHA = strings.TrimSpace(record.CommitSHA)
	record.PRURL = strings.TrimSpace(record.PRURL)
	record.ReviewVerdict = strings.TrimSpace(record.ReviewVerdict)
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
	commit_sha, pr_url, review_verdict, comment_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(idempotency_key) DO UPDATE SET
	updated_at = excluded.updated_at`,
		record.IdempotencyKey,
		record.ItemID,
		record.IssueID,
		record.ParentIssueID,
		string(record.Status),
		record.Branch,
		record.CommitSHA,
		record.PRURL,
		record.ReviewVerdict,
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
	if err := s.ensureImplementWritebackSchema(ctx); err != nil {
		return ImplementWritebackRecord{}, false, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return ImplementWritebackRecord{}, false, errors.New("startrek implement writeback idempotency key is required")
	}

	var record ImplementWritebackRecord
	var status string
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT idempotency_key, item_id, issue_id, parent_issue_id, status, branch,
	commit_sha, pr_url, review_verdict, comment_id, created_at, updated_at
FROM implement_writebacks
WHERE idempotency_key = ?`, idempotencyKey).Scan(
		&record.IdempotencyKey,
		&record.ItemID,
		&record.IssueID,
		&record.ParentIssueID,
		&status,
		&record.Branch,
		&record.CommitSHA,
		&record.PRURL,
		&record.ReviewVerdict,
		&record.CommentID,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return ImplementWritebackRecord{}, false, nil
	}
	if err != nil {
		return ImplementWritebackRecord{}, false, fmt.Errorf("get startrek implement writeback for idempotency key %q: %w", idempotencyKey, err)
	}
	record.Status = contracts.TaskStatus(status)
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}

func (s *StateStore) GetSplitSubtaskItemBySubtask(ctx context.Context, subtaskIssueID string) (SplitSubtaskItemRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return SplitSubtaskItemRecord{}, false, errors.New("startrek source state store is not initialized")
	}
	subtaskIssueID = strings.TrimSpace(subtaskIssueID)
	if subtaskIssueID == "" {
		return SplitSubtaskItemRecord{}, false, errors.New("startrek split subtask item subtask issue id is required")
	}

	var record SplitSubtaskItemRecord
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT parent_issue_id, split_task_id, subtask_issue_id, implement_item_id,
	implement_idempotency_key, split_item_id, created_at, updated_at
FROM split_subtask_items
WHERE subtask_issue_id = ?
ORDER BY updated_at DESC, parent_issue_id
LIMIT 1`, subtaskIssueID).Scan(
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
		return SplitSubtaskItemRecord{}, false, fmt.Errorf("get startrek split subtask item for subtask %q: %w", subtaskIssueID, err)
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
ORDER BY split_task_id, subtask_issue_id`, parentIssueID)
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
		return nil, fmt.Errorf("iterate startrek split subtask items for parent %q: %w", parentIssueID, err)
	}
	return records, nil
}

func (s *StateStore) RecordFinalizeSubmission(ctx context.Context, record FinalizeSubmissionRecord) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackSchema(ctx); err != nil {
		return false, err
	}
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	record.ImplementItemID = strings.TrimSpace(record.ImplementItemID)
	record.ImplementIdempotencyKey = strings.TrimSpace(record.ImplementIdempotencyKey)
	if record.ParentIssueID == "" {
		return false, errors.New("startrek finalize submission parent issue id is required")
	}
	if record.IdempotencyKey == "" {
		return false, errors.New("startrek finalize submission idempotency key is required")
	}

	now := time.Now().UTC()
	formattedNow := formatSourceStateTime(now)
	res, err := s.db.ExecContext(ctx, `
INSERT INTO finalize_submissions (
	parent_issue_id, idempotency_key, implement_item_id, implement_idempotency_key,
	created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(parent_issue_id) DO NOTHING`,
		record.ParentIssueID,
		record.IdempotencyKey,
		record.ImplementItemID,
		record.ImplementIdempotencyKey,
		formattedNow,
		formattedNow,
	)
	if err != nil {
		return false, fmt.Errorf("record startrek finalize submission for parent %q: %w", record.ParentIssueID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read startrek finalize submission insert count for parent %q: %w", record.ParentIssueID, err)
	}
	return affected > 0, nil
}

func (s *StateStore) RecordFinalizeWriteback(ctx context.Context, record FinalizeWritebackRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackSchema(ctx); err != nil {
		return err
	}
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	record.ItemID = strings.TrimSpace(record.ItemID)
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.PRURL = strings.TrimSpace(record.PRURL)
	record.CommentID = strings.TrimSpace(record.CommentID)
	if record.IdempotencyKey == "" {
		return errors.New("startrek finalize writeback idempotency key is required")
	}
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
	idempotency_key, item_id, parent_issue_id, pr_url, comment_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(idempotency_key) DO UPDATE SET
	updated_at = excluded.updated_at`,
		record.IdempotencyKey,
		record.ItemID,
		record.ParentIssueID,
		record.PRURL,
		record.CommentID,
		formattedNow,
		formattedNow,
	); err != nil {
		return fmt.Errorf("record startrek finalize writeback for idempotency key %q: %w", record.IdempotencyKey, err)
	}
	return nil
}

func (s *StateStore) GetFinalizeWriteback(ctx context.Context, idempotencyKey string) (FinalizeWritebackRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureImplementWritebackSchema(ctx); err != nil {
		return FinalizeWritebackRecord{}, false, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return FinalizeWritebackRecord{}, false, errors.New("startrek finalize writeback idempotency key is required")
	}

	var record FinalizeWritebackRecord
	var createdAt string
	var updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT idempotency_key, item_id, parent_issue_id, pr_url, comment_id, created_at, updated_at
FROM finalize_writebacks
WHERE idempotency_key = ?`, idempotencyKey).Scan(
		&record.IdempotencyKey,
		&record.ItemID,
		&record.ParentIssueID,
		&record.PRURL,
		&record.CommentID,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return FinalizeWritebackRecord{}, false, nil
	}
	if err != nil {
		return FinalizeWritebackRecord{}, false, fmt.Errorf("get startrek finalize writeback for idempotency key %q: %w", idempotencyKey, err)
	}
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}
