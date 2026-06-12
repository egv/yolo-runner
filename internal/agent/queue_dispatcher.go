package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/executor"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	defaultQueueDispatcherPollInterval = 250 * time.Millisecond
	defaultQueueDispatcherSource       = "yolo-agent-run"
)

type QueueDispatcherOptions struct {
	Preset       string
	Source       string
	Consumer     string
	PollInterval time.Duration
	MaxAttempts  int
}

type QueueDispatcher struct {
	store        *workqueue.Store
	preset       string
	source       string
	consumer     string
	pollInterval time.Duration
	maxAttempts  int
}

func NewQueueDispatcher(queuePath string, options QueueDispatcherOptions) (*QueueDispatcher, error) {
	store, err := workqueue.Open(queuePath)
	if err != nil {
		return nil, err
	}
	dispatcher := &QueueDispatcher{
		store:        store,
		preset:       strings.TrimSpace(options.Preset),
		source:       strings.TrimSpace(options.Source),
		consumer:     strings.TrimSpace(options.Consumer),
		pollInterval: options.PollInterval,
		maxAttempts:  options.MaxAttempts,
	}
	if dispatcher.source == "" {
		dispatcher.source = defaultQueueDispatcherSource
	}
	if dispatcher.consumer == "" {
		dispatcher.consumer = dispatcher.source
	}
	if dispatcher.pollInterval <= 0 {
		dispatcher.pollInterval = defaultQueueDispatcherPollInterval
	}
	return dispatcher, nil
}

func (d *QueueDispatcher) Close() error {
	if d == nil || d.store == nil {
		return nil
	}
	return d.store.Close()
}

func (d *QueueDispatcher) Submit(_ context.Context, request WorkDispatchRequest) (WorkHandle, error) {
	if d == nil || d.store == nil {
		return WorkHandle{}, fmt.Errorf("queue dispatcher is not open")
	}
	payload := normalizeQueueImplementPayload(request)
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID == "" {
		return WorkHandle{}, fmt.Errorf("queue dispatch task ID is required")
	}
	preset := d.presetForRequest(request)
	if preset == "" {
		return WorkHandle{}, fmt.Errorf("queue preset is required")
	}

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return WorkHandle{}, fmt.Errorf("marshal implement queue payload for task %q: %w", taskID, err)
	}
	item, err := d.store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         d.source,
		SourceRef:      taskID,
		IdempotencyKey: d.idempotencyKey(request, taskID),
		Preset:         preset,
		Priority:       request.Priority,
		Payload:        rawPayload,
		MaxAttempts:    d.maxAttemptsForRequest(request.Executor),
	})
	if err != nil {
		return WorkHandle{}, err
	}
	return WorkHandle{
		ID:       item.ID,
		taskID:   taskID,
		executor: request.Executor,
	}, nil
}

func (d *QueueDispatcher) AwaitResult(ctx context.Context, handle WorkHandle) (workitem.ImplementResult, error) {
	if d == nil || d.store == nil {
		return workitem.ImplementResult{}, fmt.Errorf("queue dispatcher is not open")
	}
	itemID := strings.TrimSpace(handle.ID)
	if itemID == "" {
		return workitem.ImplementResult{}, fmt.Errorf("queue handle ID is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		results, err := d.store.ListUnconsumedResults(d.source)
		if err != nil {
			return workitem.ImplementResult{}, err
		}
		for _, queued := range results {
			if queued.Item.ID != itemID {
				continue
			}
			result, err := decodeQueueImplementResult(queued.Result)
			if err != nil {
				return workitem.ImplementResult{}, err
			}
			if err := applyQueueImplementResult(ctx, handle, result); err != nil {
				return workitem.ImplementResult{}, err
			}
			if err := d.store.MarkConsumed(itemID, d.consumer); err != nil {
				return workitem.ImplementResult{}, err
			}
			return result, nil
		}

		timer := time.NewTimer(d.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return workitem.ImplementResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func normalizeQueueImplementPayload(request WorkDispatchRequest) workitem.ImplementPayload {
	payload := request.Payload
	if strings.TrimSpace(payload.TaskID) == "" {
		payload.TaskID = strings.TrimSpace(request.Task.ID)
	}
	if strings.TrimSpace(payload.Title) == "" {
		payload.Title = strings.TrimSpace(request.Task.Title)
	}
	if strings.TrimSpace(payload.Description) == "" {
		payload.Description = request.Task.Description
	}
	if strings.TrimSpace(payload.PromptContext.ParentID) == "" {
		payload.PromptContext.ParentID = strings.TrimSpace(request.Task.ParentID)
	}
	if len(payload.PromptContext.Metadata) == 0 && len(request.Task.Metadata) > 0 {
		payload.PromptContext.Metadata = cloneStringMap(request.Task.Metadata)
	}
	return payload
}

func (d *QueueDispatcher) presetForRequest(request WorkDispatchRequest) string {
	for _, value := range []string{
		d.preset,
		request.Payload.PromptContext.Metadata["queue_preset"],
		request.Payload.PromptContext.Metadata["preset"],
		request.Task.Metadata["queue_preset"],
		request.Task.Metadata["preset"],
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (d *QueueDispatcher) idempotencyKey(request WorkDispatchRequest, taskID string) string {
	parentID := strings.TrimSpace(request.Payload.PromptContext.ParentID)
	if parentID == "" {
		parentID = strings.TrimSpace(request.Task.ParentID)
	}
	parts := []string{d.source}
	if parentID != "" {
		parts = append(parts, parentID)
	}
	parts = append(parts, taskID, string(workitem.KindImplement))
	return strings.Join(parts, "/")
}

func (d *QueueDispatcher) maxAttemptsForRequest(exec *executor.Executor) int {
	if exec != nil && exec.MaxRetries >= 0 {
		return exec.MaxRetries + 1
	}
	return d.maxAttempts
}

func decodeQueueImplementResult(result workqueue.Result) (workitem.ImplementResult, error) {
	decoded, err := workitem.DecodeImplementResult(result.Payload)
	if err != nil {
		return workitem.ImplementResult{}, fmt.Errorf("decode implement queue result for item %q: %w", result.ItemID, err)
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

func applyQueueImplementResult(ctx context.Context, handle WorkHandle, result workitem.ImplementResult) error {
	exec := handle.executor
	if exec == nil {
		return nil
	}
	taskID := strings.TrimSpace(handle.taskID)
	if taskID == "" {
		return fmt.Errorf("queue result task ID is required")
	}

	switch contracts.RunnerResultStatus(strings.TrimSpace(result.Status)) {
	case contracts.RunnerResultCompleted:
		if exec.Tasks != nil {
			return exec.Tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusClosed)
		}
	case contracts.RunnerResultBlocked:
		data := queueResultTaskData("blocked", result)
		if exec.MarkTaskBlockedWithData != nil {
			if err := exec.MarkTaskBlockedWithData(taskID, data); err != nil {
				return err
			}
		}
		if exec.Tasks != nil {
			if err := exec.Tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusBlocked); err != nil {
				return err
			}
			if err := exec.Tasks.SetTaskData(ctx, taskID, data); err != nil {
				return err
			}
		}
	case contracts.RunnerResultFailed:
		data := queueResultTaskData("failed", result)
		if exec.Tasks != nil {
			if err := exec.Tasks.SetTaskData(ctx, taskID, data); err != nil {
				return err
			}
			if err := exec.Tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusFailed); err != nil {
				return err
			}
		}
		if exec.ClearTaskInFlight != nil {
			return exec.ClearTaskInFlight(taskID)
		}
		if exec.ClearTaskTerminalState != nil {
			return exec.ClearTaskTerminalState(taskID)
		}
	default:
		data := queueResultTaskData("failed", result)
		if strings.TrimSpace(data["triage_reason"]) == "" {
			data["triage_reason"] = fmt.Sprintf("invalid queued implement result status %q", result.Status)
		}
		if exec.Tasks != nil {
			if err := exec.Tasks.SetTaskData(ctx, taskID, data); err != nil {
				return err
			}
			if err := exec.Tasks.SetTaskStatus(ctx, taskID, contracts.TaskStatusFailed); err != nil {
				return err
			}
		}
		if exec.ClearTaskInFlight != nil {
			return exec.ClearTaskInFlight(taskID)
		}
		if exec.ClearTaskTerminalState != nil {
			return exec.ClearTaskTerminalState(taskID)
		}
	}
	return nil
}

func queueResultTaskData(status string, result workitem.ImplementResult) map[string]string {
	data := map[string]string{"triage_status": strings.TrimSpace(status)}
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		data["triage_reason"] = reason
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
