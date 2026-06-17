package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const defaultSourceName = "beads"

type Source struct {
	SourceName  string
	RootID      string
	Preset      string
	Priority    int
	MaxAttempts int
	Storage     contracts.StorageBackend
	ReadyLister ReadyTaskLister
	Engine      contracts.TaskEngine
	Queue       *workqueue.Store
}

type ReadyTaskLister interface {
	ReadyTasks(context.Context) ([]contracts.Task, error)
}

type plannedSubmission struct {
	taskID     string
	submission workqueue.Submission
	depTaskIDs []string
}

func (s *Source) Name() string {
	return fallbackText(s.SourceName, defaultSourceName)
}

func (s *Source) Poll(ctx context.Context) ([]workqueue.Submission, error) {
	if s == nil {
		return nil, errors.New("beads source is required")
	}
	terminalItems, err := s.terminalQueueItemIDsByTaskID()
	if err != nil {
		return nil, err
	}
	planned, err := s.plan(ctx, terminalItems)
	if err != nil {
		return nil, err
	}
	return s.enqueuePlanned(planned, terminalItems)
}

func (s *Source) Reconcile(ctx context.Context) ([]workqueue.Submission, error) {
	if s == nil {
		return nil, errors.New("beads source is required")
	}
	if s.Queue == nil {
		return nil, errors.New("beads source queue is required")
	}
	if _, err := s.Queue.RequeueStale(time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("requeue stale beads work items: %w", err)
	}
	return s.Poll(ctx)
}

func (s *Source) HandleResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if item.Kind != workitem.KindImplement {
		return nil, nil
	}
	if s == nil {
		return nil, errors.New("beads source is required")
	}
	if s.Storage == nil {
		return nil, errors.New("beads source storage backend is required")
	}

	taskID := strings.TrimSpace(item.SourceRef)
	if taskID == "" {
		payload, err := decodeImplementPayload(item.Payload)
		if err != nil {
			return nil, err
		}
		taskID = strings.TrimSpace(payload.TaskID)
	}
	if taskID == "" {
		return nil, errors.New("beads result task id is required")
	}

	implementResult, err := decodeImplementResult(result)
	if err != nil {
		return nil, err
	}
	status, data := taskUpdateFromImplementResult(implementResult)
	if len(data) > 0 {
		if err := s.Storage.SetTaskData(ctx, taskID, data); err != nil {
			return nil, fmt.Errorf("write beads result data for %q: %w", taskID, err)
		}
	}
	if err := s.Storage.SetTaskStatus(ctx, taskID, status); err != nil {
		return nil, fmt.Errorf("set beads task %q status to %q: %w", taskID, status, err)
	}
	return nil, nil
}

func (s *Source) plan(ctx context.Context, terminalItems map[string]string) ([]plannedSubmission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("beads source is required")
	}
	rootID := strings.TrimSpace(s.RootID)
	preset := strings.TrimSpace(s.Preset)
	if preset == "" {
		return nil, errors.New("beads source preset is required")
	}
	if rootID == "" {
		return s.planWorkspaceReady(ctx, terminalItems)
	}
	if s.Storage == nil {
		return nil, errors.New("beads source storage backend is required")
	}

	tree, err := s.Storage.GetTaskTree(ctx, rootID)
	if err != nil {
		return nil, fmt.Errorf("load beads task tree %q: %w", rootID, err)
	}
	graph, err := s.taskEngine().BuildGraph(tree)
	if err != nil {
		return nil, fmt.Errorf("build beads task graph %q: %w", rootID, err)
	}

	nodes := topologicallyOrderedOpenLeafNodes(graph)
	planned := make([]plannedSubmission, 0, len(nodes))
	for _, node := range nodes {
		if _, terminal := terminalItems[node.ID]; terminal {
			continue
		}
		hasOpenItem, err := s.hasOpenQueueItem(node.ID)
		if err != nil {
			return nil, err
		}
		if hasOpenItem {
			continue
		}
		submission, err := s.submissionForTask(rootID, node.Task, node.Priority)
		if err != nil {
			return nil, err
		}
		planned = append(planned, plannedSubmission{
			taskID:     node.ID,
			submission: submission,
			depTaskIDs: dependencyTaskIDs(node),
		})
	}
	return planned, nil
}

func (s *Source) planWorkspaceReady(ctx context.Context, terminalItems map[string]string) ([]plannedSubmission, error) {
	lister := s.readyTaskLister()
	if lister == nil {
		return nil, errors.New("beads source ready task lister is required when root id is omitted")
	}
	tasks, err := lister.ReadyTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ready beads tasks: %w", err)
	}

	planned := make([]plannedSubmission, 0, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		if _, terminal := terminalItems[taskID]; terminal {
			continue
		}
		hasOpenItem, err := s.hasOpenQueueItem(taskID)
		if err != nil {
			return nil, err
		}
		if hasOpenItem {
			continue
		}
		submission, err := s.submissionForTask("", task, taskMetadataPriority(task))
		if err != nil {
			return nil, err
		}
		planned = append(planned, plannedSubmission{
			taskID:     taskID,
			submission: submission,
		})
	}
	return planned, nil
}

func (s *Source) enqueuePlanned(planned []plannedSubmission, terminalItems map[string]string) ([]workqueue.Submission, error) {
	submissions := make([]workqueue.Submission, 0, len(planned))
	if s.Queue == nil {
		for _, plan := range planned {
			submissions = append(submissions, plan.submission)
		}
		return submissions, nil
	}

	itemIDsByTaskID := cloneStringMap(terminalItems)
	if itemIDsByTaskID == nil {
		itemIDsByTaskID = map[string]string{}
	}
	for _, plan := range planned {
		depItemIDs := make([]string, 0, len(plan.depTaskIDs))
		for _, depTaskID := range plan.depTaskIDs {
			if itemID := strings.TrimSpace(itemIDsByTaskID[depTaskID]); itemID != "" {
				depItemIDs = append(depItemIDs, itemID)
			}
		}
		queued, err := s.Queue.EnqueueWithDeps(plan.submission, depItemIDs)
		if err != nil {
			return nil, fmt.Errorf("enqueue beads task %q: %w", plan.taskID, err)
		}
		itemIDsByTaskID[plan.taskID] = queued.ID
		if isTerminalQueueState(queued.State) {
			continue
		}
		submissions = append(submissions, plan.submission)
	}
	return submissions, nil
}

func (s *Source) hasOpenQueueItem(taskID string) (bool, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || s == nil || s.Queue == nil {
		return false, nil
	}
	hasOpenItem, err := s.Queue.HasOpenItem(s.Name(), taskID)
	if err != nil {
		return false, fmt.Errorf("check open beads queue item for task %q: %w", taskID, err)
	}
	return hasOpenItem, nil
}

func isTerminalQueueState(state string) bool {
	switch strings.TrimSpace(state) {
	case "done", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func (s *Source) terminalQueueItemIDsByTaskID() (map[string]string, error) {
	if s == nil || s.Queue == nil {
		return nil, nil
	}
	results, err := s.Queue.ListUnconsumedResults(s.Name())
	if err != nil {
		return nil, fmt.Errorf("list terminal beads queue results: %w", err)
	}
	itemIDsByTaskID := map[string]string{}
	for _, result := range results {
		if result.Item.Kind != workitem.KindImplement {
			continue
		}
		taskID := strings.TrimSpace(result.Item.SourceRef)
		if taskID == "" {
			payload, err := decodeImplementPayload(result.Item.Payload)
			if err != nil {
				return nil, err
			}
			taskID = strings.TrimSpace(payload.TaskID)
		}
		if taskID != "" {
			itemIDsByTaskID[taskID] = result.Item.ID
		}
	}
	if len(itemIDsByTaskID) == 0 {
		return nil, nil
	}
	return itemIDsByTaskID, nil
}

func (s *Source) submissionForTask(rootID string, task contracts.Task, graphPriority int) (workqueue.Submission, error) {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return workqueue.Submission{}, errors.New("beads task id is required")
	}
	metadata := cloneStringMap(task.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if rootID = strings.TrimSpace(rootID); rootID != "" {
		metadata["queue_root"] = rootID
	}

	payload, err := json.Marshal(workitem.ImplementPayload{
		TaskID:      taskID,
		Title:       strings.TrimSpace(task.Title),
		Description: task.Description,
		PromptContext: workitem.ImplementPromptContext{
			ParentID: strings.TrimSpace(task.ParentID),
			Metadata: metadata,
		},
	})
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("encode beads implement payload for %q: %w", taskID, err)
	}

	priority := s.Priority
	if graphPriority != 0 {
		priority = graphPriority
	}
	return workqueue.Submission{
		Kind:           workitem.KindImplement,
		Source:         s.Name(),
		SourceRef:      taskID,
		IdempotencyKey: beadsIdempotencyKey(s.Name(), rootID, taskID),
		Preset:         strings.TrimSpace(s.Preset),
		Priority:       priority,
		Payload:        payload,
		MaxAttempts:    s.MaxAttempts,
	}, nil
}

func (s *Source) taskEngine() contracts.TaskEngine {
	if s.Engine != nil {
		return s.Engine
	}
	return engine.NewTaskEngine()
}

func (s *Source) readyTaskLister() ReadyTaskLister {
	if s == nil {
		return nil
	}
	if s.ReadyLister != nil {
		return s.ReadyLister
	}
	lister, _ := s.Storage.(ReadyTaskLister)
	return lister
}

func topologicallyOrderedOpenLeafNodes(graph *contracts.TaskGraph) []*contracts.TaskNode {
	if graph == nil || len(graph.Nodes) == 0 {
		return nil
	}

	ids := make([]string, 0, len(graph.Nodes))
	for id := range graph.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	visited := map[string]bool{}
	ordered := make([]*contracts.TaskNode, 0, len(ids))
	var visit func(string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true

		node := graph.Nodes[id]
		if node == nil {
			return
		}
		deps := append([]*contracts.TaskNode(nil), node.Dependencies...)
		sort.SliceStable(deps, func(i, j int) bool {
			return deps[i].ID < deps[j].ID
		})
		for _, dep := range deps {
			if dep != nil {
				visit(dep.ID)
			}
		}
		if shouldSubmitNode(graph, node) {
			ordered = append(ordered, node)
		}
	}

	for _, id := range ids {
		visit(id)
	}
	return ordered
}

func shouldSubmitNode(graph *contracts.TaskGraph, node *contracts.TaskNode) bool {
	if graph == nil || node == nil {
		return false
	}
	if strings.TrimSpace(node.ID) == strings.TrimSpace(graph.RootID) {
		return false
	}
	if len(node.Children) > 0 {
		return false
	}
	return node.Status == contracts.TaskStatusOpen
}

func dependencyTaskIDs(node *contracts.TaskNode) []string {
	if node == nil || len(node.Dependencies) == 0 {
		return nil
	}
	ids := make([]string, 0, len(node.Dependencies))
	for _, dep := range node.Dependencies {
		if dep == nil || dep.Status != contracts.TaskStatusOpen || len(dep.Children) > 0 {
			continue
		}
		if id := strings.TrimSpace(dep.ID); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func taskMetadataPriority(task contracts.Task) int {
	if task.Metadata == nil {
		return 0
	}
	raw := strings.TrimSpace(task.Metadata["priority"])
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func decodeImplementPayload(raw json.RawMessage) (workitem.ImplementPayload, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	return workitem.DecodeImplementPayload(raw)
}

func decodeImplementResult(result workqueue.Result) (workitem.ImplementResult, error) {
	raw := result.Payload
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoded, err := workitem.DecodeImplementResult(raw)
	if err != nil {
		return workitem.ImplementResult{}, fmt.Errorf("decode beads implement result for item %q: %w", result.ItemID, err)
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

func taskUpdateFromImplementResult(result workitem.ImplementResult) (contracts.TaskStatus, map[string]string) {
	status := strings.TrimSpace(result.Status)
	switch contracts.RunnerResultStatus(status) {
	case contracts.RunnerResultCompleted:
		return contracts.TaskStatusClosed, terminalResultData("", result)
	case contracts.RunnerResultBlocked:
		return contracts.TaskStatusBlocked, terminalResultData("blocked", result)
	case contracts.RunnerResultFailed:
		return contracts.TaskStatusFailed, terminalResultData("failed", result)
	default:
		if strings.TrimSpace(result.Reason) == "" {
			result.Reason = fmt.Sprintf("invalid implement result status %q", status)
		}
		return contracts.TaskStatusFailed, terminalResultData("failed", result)
	}
}

func terminalResultData(decision string, result workitem.ImplementResult) map[string]string {
	data := map[string]string{}
	if decision = strings.TrimSpace(decision); decision != "" {
		data["triage_status"] = decision
		data["decision"] = decision
	}
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		data["reason"] = reason
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
	for key, value := range result.Artifacts {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			data[key] = value
		}
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

func beadsIdempotencyKey(sourceName string, rootID string, taskID string) string {
	parts := []string{strings.TrimSpace(sourceName)}
	if rootID = strings.TrimSpace(rootID); rootID != "" {
		parts = append(parts, rootID)
	}
	parts = append(parts, strings.TrimSpace(taskID), string(workitem.KindImplement))
	return strings.Join(parts, "/")
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func fallbackText(primary string, fallback string) string {
	primary = strings.TrimSpace(primary)
	if primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}
