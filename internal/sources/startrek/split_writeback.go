package startrek

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type SplitSubtaskItemRecord struct {
	ParentIssueID           string    `json:"parent_issue_id"`
	SplitTaskID             string    `json:"split_task_id"`
	SubtaskIssueID          string    `json:"subtask_issue_id"`
	ImplementItemID         string    `json:"implement_item_id"`
	ImplementIdempotencyKey string    `json:"implement_idempotency_key"`
	SplitItemID             string    `json:"split_item_id"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (s *Source) handleSplitResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	if s.Tracker == nil {
		return nil, errors.New("startrek split writeback tracker is required")
	}
	if s.State == nil {
		return nil, errors.New("startrek source state store is required")
	}
	if s.Queue == nil {
		return nil, errors.New("startrek split writeback queue is required")
	}
	if result.Status != "" && result.Status != workqueue.ResultStatusCompleted {
		return nil, fmt.Errorf("startrek split result for item %q has unsupported status %q", item.ID, result.Status)
	}
	if strings.TrimSpace(item.IdempotencyKey) == "" {
		return nil, errors.New("startrek split item idempotency key is required")
	}

	payload, err := decodeSplitPayload(item)
	if err != nil {
		return nil, err
	}
	splitResult, err := decodeSplitResult(item, result)
	if err != nil {
		return nil, err
	}
	output := splitResult.ToStrictOutput()

	parent := payload.Task.ToTask()
	parentID := splitIssueID(item, payload)
	if parentID == "" {
		return nil, errors.New("startrek split parent issue id is required")
	}
	if strings.TrimSpace(parent.ID) == "" {
		parent.ID = parentID
	}
	queueKey := strings.TrimSpace(payload.QueueRoot.ID)

	tracker, ok := s.Tracker.(trackerstartrek.IdempotentSplitSubtaskCreationTracker)
	if !ok {
		return nil, errors.New("startrek split writeback tracker does not support idempotent subtask creation")
	}
	subtasks, err := (trackerstartrek.IdempotentSplitSubtaskCreationService{
		Tracker:      tracker,
		ReadyLabel:   s.readyLabel(),
		SplitVersion: s.splitVersion(),
	}).Create(ctx, trackerstartrek.SplitSubtasksInput{
		QueueKey:          queueKey,
		ParentID:          parentID,
		ParentTitle:       parent.Title,
		ParentDescription: parent.Description,
		Output:            output,
	})
	if err != nil {
		return nil, err
	}
	if err := s.Tracker.RemoveLabel(ctx, parentID, s.readyLabel()); err != nil {
		return nil, fmt.Errorf("remove startrek ready label from split parent %q: %w", parentID, err)
	}

	tasks, err := orderedSplitWritebackTasks(output.Tasks, output.Order)
	if err != nil {
		return nil, err
	}
	submissions, err := s.enqueueSplitImplementSubmissions(ctx, item, parent, tasks, subtasks.IssueIDsBySplitTaskID)
	if err != nil {
		return nil, err
	}
	return submissions, nil
}

func (s *StateStore) RecordSplitSubtaskItem(ctx context.Context, record SplitSubtaskItemRecord) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return errors.New("startrek source state store is not initialized")
	}
	record.ParentIssueID = strings.TrimSpace(record.ParentIssueID)
	record.SplitTaskID = strings.TrimSpace(record.SplitTaskID)
	record.SubtaskIssueID = strings.TrimSpace(record.SubtaskIssueID)
	record.ImplementItemID = strings.TrimSpace(record.ImplementItemID)
	record.ImplementIdempotencyKey = strings.TrimSpace(record.ImplementIdempotencyKey)
	record.SplitItemID = strings.TrimSpace(record.SplitItemID)
	if record.ParentIssueID == "" {
		return errors.New("startrek split subtask item parent issue id is required")
	}
	if record.SplitTaskID == "" {
		return errors.New("startrek split subtask item split task id is required")
	}
	if record.SubtaskIssueID == "" {
		return errors.New("startrek split subtask item subtask issue id is required")
	}
	if record.ImplementItemID == "" {
		return errors.New("startrek split subtask item implement item id is required")
	}
	if record.ImplementIdempotencyKey == "" {
		return errors.New("startrek split subtask item implement idempotency key is required")
	}

	now := time.Now().UTC()
	formattedNow := formatSourceStateTime(now)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO split_subtask_items (
	parent_issue_id, split_task_id, subtask_issue_id, implement_item_id,
	implement_idempotency_key, split_item_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(parent_issue_id, subtask_issue_id) DO UPDATE SET
	split_task_id = excluded.split_task_id,
	implement_item_id = excluded.implement_item_id,
	implement_idempotency_key = excluded.implement_idempotency_key,
	split_item_id = excluded.split_item_id,
	updated_at = excluded.updated_at`,
		record.ParentIssueID,
		record.SplitTaskID,
		record.SubtaskIssueID,
		record.ImplementItemID,
		record.ImplementIdempotencyKey,
		record.SplitItemID,
		formattedNow,
		formattedNow,
	); err != nil {
		return fmt.Errorf("record startrek split subtask item for parent %q subtask %q: %w", record.ParentIssueID, record.SubtaskIssueID, err)
	}
	return nil
}

func (s *StateStore) GetSplitSubtaskItem(ctx context.Context, parentIssueID string, subtaskIssueID string) (SplitSubtaskItemRecord, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.db == nil {
		return SplitSubtaskItemRecord{}, false, errors.New("startrek source state store is not initialized")
	}
	parentIssueID = strings.TrimSpace(parentIssueID)
	subtaskIssueID = strings.TrimSpace(subtaskIssueID)
	if parentIssueID == "" {
		return SplitSubtaskItemRecord{}, false, errors.New("startrek split subtask item parent issue id is required")
	}
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
WHERE parent_issue_id = ? AND subtask_issue_id = ?`,
		parentIssueID,
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
		return SplitSubtaskItemRecord{}, false, fmt.Errorf("get startrek split subtask item for parent %q subtask %q: %w", parentIssueID, subtaskIssueID, err)
	}
	record.CreatedAt = parseSourceStateTime(createdAt)
	record.UpdatedAt = parseSourceStateTime(updatedAt)
	return record, true, nil
}

func (s *Source) splitVersion() string {
	return strings.TrimSpace(s.SplitVersion)
}

func (s *Source) enqueueSplitImplementSubmissions(ctx context.Context, splitItem workitem.Item, parent contracts.Task, tasks []splitter.Task, issueIDsBySplitTaskID map[string]string) ([]workqueue.Submission, error) {
	submissions := make([]workqueue.Submission, 0, len(tasks))
	itemIDsBySplitTaskID := make(map[string]string, len(tasks))
	for _, task := range tasks {
		taskID := trimSplitWritebackRef(task.ID)
		subtaskIssueID := strings.TrimSpace(issueIDsBySplitTaskID[taskID])
		if subtaskIssueID == "" {
			return nil, fmt.Errorf("startrek split result is missing subtask issue id for split task %q", taskID)
		}

		submission, err := splitImplementSubmission(splitItem, parent, task, subtaskIssueID, s.Name())
		if err != nil {
			return nil, err
		}
		depItemIDs := splitDependencyItemIDs(task.DependsOn, itemIDsBySplitTaskID)
		queued, err := s.Queue.EnqueueWithDeps(submission, depItemIDs)
		if err != nil {
			return nil, fmt.Errorf("enqueue startrek split implement item for subtask %q: %w", subtaskIssueID, err)
		}
		itemIDsBySplitTaskID[taskID] = queued.ID
		if err := s.State.RecordSplitSubtaskItem(ctx, SplitSubtaskItemRecord{
			ParentIssueID:           parent.ID,
			SplitTaskID:             taskID,
			SubtaskIssueID:          subtaskIssueID,
			ImplementItemID:         queued.ID,
			ImplementIdempotencyKey: submission.IdempotencyKey,
			SplitItemID:             splitItem.ID,
		}); err != nil {
			return nil, err
		}
		submissions = append(submissions, submission)
	}
	return submissions, nil
}

func splitImplementSubmission(splitItem workitem.Item, parent contracts.Task, task splitter.Task, subtaskIssueID string, sourceName string) (workqueue.Submission, error) {
	key, err := splitFollowUpIdempotencyKey(splitItem.IdempotencyKey, subtaskIssueID, string(workitem.KindImplement))
	if err != nil {
		return workqueue.Submission{}, err
	}
	payload, err := json.Marshal(workitem.ImplementPayload{
		TaskID:      subtaskIssueID,
		Title:       splitWritebackSubtaskTitle(task),
		Description: splitWritebackSubtaskDescription(task),
		PromptContext: workitem.ImplementPromptContext{
			ParentID: strings.TrimSpace(parent.ID),
			Metadata: splitImplementMetadata(parent.Metadata, task.ID),
		},
	})
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("encode startrek split implement follow-up for subtask %q: %w", subtaskIssueID, err)
	}
	return workqueue.Submission{
		Kind:           workitem.KindImplement,
		Source:         sourceName,
		SourceRef:      subtaskIssueID,
		IdempotencyKey: key,
		Preset:         strings.TrimSpace(splitItem.Preset),
		Priority:       splitItem.Priority,
		Payload:        payload,
		MaxAttempts:    splitItem.MaxAttempts,
	}, nil
}

func splitFollowUpIdempotencyKey(splitKey string, issueID string, stage string) (string, error) {
	splitKey = strings.TrimSpace(splitKey)
	issueID = strings.TrimSpace(issueID)
	stage = strings.TrimSpace(stage)
	parts := strings.SplitN(splitKey, "/", 4)
	if len(parts) != 4 || parts[0] != "st" || parts[2] != "split" || strings.TrimSpace(parts[3]) == "" {
		return "", fmt.Errorf("startrek split idempotency key %q must match st/<issue>/split/<rev>", splitKey)
	}
	if issueID == "" || stage == "" {
		return "", errors.New("startrek split follow-up idempotency key requires issue id and stage")
	}
	return "st/" + issueID + "/" + stage + "/" + strings.TrimSpace(parts[3]), nil
}

func splitImplementMetadata(parentMetadata map[string]string, splitTaskID string) map[string]string {
	metadata := cloneStartrekStringMap(parentMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if taskID := trimSplitWritebackRef(splitTaskID); taskID != "" {
		metadata["split_task_id"] = taskID
	}
	return metadata
}

func splitDependencyItemIDs(dependsOn []string, itemIDsBySplitTaskID map[string]string) []string {
	itemIDs := make([]string, 0, len(dependsOn))
	seen := map[string]bool{}
	for _, dependency := range dependsOn {
		dependencyID := trimSplitWritebackRef(dependency)
		if dependencyID == "" || isSplitWritebackNone(dependencyID) {
			continue
		}
		itemID := strings.TrimSpace(itemIDsBySplitTaskID[dependencyID])
		if itemID == "" || seen[itemID] {
			continue
		}
		seen[itemID] = true
		itemIDs = append(itemIDs, itemID)
	}
	return itemIDs
}

func orderedSplitWritebackTasks(tasks []splitter.Task, order []splitter.Dependency) ([]splitter.Task, error) {
	return topologicallyOrderSplitWritebackTasks(splitWritebackTasksWithOrderDependencies(tasks, order))
}

func splitWritebackTasksWithOrderDependencies(tasks []splitter.Task, order []splitter.Dependency) []splitter.Task {
	if len(tasks) == 0 || len(order) == 0 {
		return cloneSplitWritebackTasks(tasks)
	}

	withDependencies := cloneSplitWritebackTasks(tasks)
	taskIndexByID := make(map[string]int, len(withDependencies))
	for i, task := range withDependencies {
		if id := trimSplitWritebackRef(task.ID); id != "" {
			taskIndexByID[id] = i
		}
	}
	for _, dependency := range order {
		fromID := trimSplitWritebackRef(dependency.From)
		toID := trimSplitWritebackRef(dependency.To)
		if fromID == "" || toID == "" || isSplitWritebackNone(fromID) || isSplitWritebackNone(toID) {
			continue
		}
		toIndex, ok := taskIndexByID[toID]
		if !ok {
			continue
		}
		if _, ok := taskIndexByID[fromID]; !ok {
			continue
		}
		withDependencies[toIndex].DependsOn = appendSplitWritebackDependency(withDependencies[toIndex].DependsOn, fromID)
	}
	return withDependencies
}

func topologicallyOrderSplitWritebackTasks(tasks []splitter.Task) ([]splitter.Task, error) {
	byID := make(map[string]splitter.Task, len(tasks))
	inputOrder := make([]string, 0, len(tasks))
	for _, task := range tasks {
		id := trimSplitWritebackRef(task.ID)
		if id == "" {
			return nil, errors.New("split task id is required")
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf("duplicate split task id %q", id)
		}
		task.ID = id
		task.DependsOn = cloneStartrekStringSlice(task.DependsOn)
		byID[id] = task
		inputOrder = append(inputOrder, id)
	}

	visited := make(map[string]bool, len(tasks))
	visiting := make(map[string]bool, len(tasks))
	ordered := make([]splitter.Task, 0, len(tasks))
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("cyclic split subtask dependency involving %q", id)
		}
		task := byID[id]
		visiting[id] = true
		for _, dependency := range task.DependsOn {
			dependencyID := trimSplitWritebackRef(dependency)
			if dependencyID == "" || isSplitWritebackNone(dependencyID) {
				continue
			}
			if _, ok := byID[dependencyID]; ok {
				if err := visit(dependencyID); err != nil {
					return err
				}
			}
		}
		visiting[id] = false
		visited[id] = true
		ordered = append(ordered, task)
		return nil
	}
	for _, id := range inputOrder {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func appendSplitWritebackDependency(dependencies []string, dependencyID string) []string {
	dependencyID = trimSplitWritebackRef(dependencyID)
	if dependencyID == "" || isSplitWritebackNone(dependencyID) {
		return dependencies
	}
	for _, existing := range dependencies {
		if trimSplitWritebackRef(existing) == dependencyID {
			return dependencies
		}
	}
	return append(dependencies, dependencyID)
}

func splitWritebackSubtaskTitle(task splitter.Task) string {
	id := trimSplitWritebackRef(task.ID)
	title := strings.TrimSpace(task.Title)
	if title == "" {
		return id
	}
	if id == "" || strings.HasPrefix(title, id+" ") {
		return title
	}
	return id + " " + title
}

func splitWritebackSubtaskDescription(task splitter.Task) string {
	var b strings.Builder
	b.WriteString("### Task: ")
	b.WriteString(splitWritebackSubtaskTitle(task))
	b.WriteString("\n\n")
	writeSplitWritebackSection(&b, "Why", task.Why)
	writeSplitWritebackSection(&b, "In scope", task.InScope)
	writeSplitWritebackSection(&b, "Out of scope", task.OutOfScope)
	writeSplitWritebackSection(&b, "Strict TDD", task.StrictTDD)
	writeSplitWritebackSection(&b, "Done when", task.DoneWhen)
	writeSplitWritebackSection(&b, "Expected files", task.ExpectedFiles)
	writeSplitWritebackSection(&b, "Depends on", task.DependsOn)
	writeSplitWritebackSection(&b, "Unlocks", task.Unlocks)
	return strings.TrimSpace(b.String())
}

func writeSplitWritebackSection(b *strings.Builder, label string, values []string) {
	normalized := normalizedSplitWritebackItems(values)
	if len(normalized) == 0 {
		return
	}
	b.WriteString(label)
	b.WriteString(":\n")
	for _, value := range normalized {
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func normalizedSplitWritebackItems(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func cloneSplitWritebackTasks(tasks []splitter.Task) []splitter.Task {
	if tasks == nil {
		return nil
	}
	out := make([]splitter.Task, len(tasks))
	for i, task := range tasks {
		out[i] = task
		out[i].Why = cloneStartrekStringSlice(task.Why)
		out[i].InScope = cloneStartrekStringSlice(task.InScope)
		out[i].OutOfScope = cloneStartrekStringSlice(task.OutOfScope)
		out[i].StrictTDD = cloneStartrekStringSlice(task.StrictTDD)
		out[i].DoneWhen = cloneStartrekStringSlice(task.DoneWhen)
		out[i].ExpectedFiles = cloneStartrekStringSlice(task.ExpectedFiles)
		out[i].DependsOn = cloneStartrekStringSlice(task.DependsOn)
		out[i].Unlocks = cloneStartrekStringSlice(task.Unlocks)
	}
	return out
}

func cloneStartrekStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func trimSplitWritebackRef(value string) string {
	return strings.Trim(strings.TrimSpace(value), "`")
}

func isSplitWritebackNone(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "none")
}

func decodeSplitPayload(item workitem.Item) (workitem.SplitPayload, error) {
	var payload workitem.SplitPayload
	if len(item.Payload) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return workitem.SplitPayload{}, fmt.Errorf("decode startrek split item payload %q: %w", item.ID, err)
	}
	return payload, nil
}

func decodeSplitResult(item workitem.Item, result workqueue.Result) (workitem.SplitResult, error) {
	var splitResult workitem.SplitResult
	if err := json.Unmarshal(result.Payload, &splitResult); err != nil {
		return workitem.SplitResult{}, fmt.Errorf("decode startrek split result for item %q: %w", item.ID, err)
	}
	return splitResult, nil
}

func splitIssueID(item workitem.Item, payload workitem.SplitPayload) string {
	if sourceRef := strings.TrimSpace(item.SourceRef); sourceRef != "" {
		return sourceRef
	}
	return strings.TrimSpace(payload.Task.ID)
}
