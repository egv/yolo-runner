package startrek

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const defaultStorageReadyLabel = "yolo-agent-ready"

// StorageBackend adapts Startrek issues to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	client            *Client
	readyLabel        string
	statusTransitions StatusTransitionNames
	searchPerPage     int
}

var _ contracts.StorageBackend = (*StorageBackend)(nil)

func NewStorageBackend(cfg Config) (*StorageBackend, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &StorageBackend{
		client:            client,
		readyLabel:        strings.TrimSpace(cfg.ReadyLabel),
		statusTransitions: trimStatusTransitions(cfg.StatusTransitions),
	}, nil
}

// QueueSearchOptions carries the per-queue discovery trigger into the backend.
type QueueSearchOptions struct {
	QueueKey string
	Assignee string
	Label    string
}

// GetTaskTreeForQueue is the discovery entry point: it searches the queue for
// issues assigned to Assignee and carrying Label, then builds the task tree
// from the native Startrek workflow status.
func (b *StorageBackend) GetTaskTreeForQueue(ctx context.Context, opts QueueSearchOptions) (*contracts.TaskTree, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}
	queueKey := strings.TrimSpace(opts.QueueKey)
	if queueKey == "" {
		return nil, errors.New("startrek queue key is required")
	}
	label := strings.TrimSpace(opts.Label)
	if label == "" {
		label = b.effectiveReadyLabel()
	}
	return b.buildTaskTree(ctx, queueKey, label, strings.TrimSpace(opts.Assignee))
}

// GetTaskTree satisfies contracts.StorageBackend. It is retained for callers
// that build a tree from an explicit root without the per-queue discovery
// trigger; discovery uses GetTaskTreeForQueue instead.
func (b *StorageBackend) GetTaskTree(ctx context.Context, queueKey string) (*contracts.TaskTree, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}
	queueKey = strings.TrimSpace(queueKey)
	if queueKey == "" {
		return nil, errors.New("startrek queue key is required")
	}
	return b.buildTaskTree(ctx, queueKey, b.effectiveReadyLabel(), "")
}

func (b *StorageBackend) buildTaskTree(ctx context.Context, queueKey string, label string, assignee string) (*contracts.TaskTree, error) {
	root := contracts.Task{
		ID:     queueKey,
		Title:  queueKey,
		Status: contracts.TaskStatusOpen,
	}
	tasks := map[string]contracts.Task{
		root.ID: root,
	}
	relations := make([]contracts.TaskRelation, 0)
	seenRelations := map[string]struct{}{}

	perPage := b.searchPerPage
	if perPage <= 0 {
		perPage = defaultIssueSearchPerPage
	}

	issuesBySearchID := map[string]Issue{}
	page := defaultIssueSearchPage
	for {
		result, err := b.client.SearchIssues(ctx, IssueSearchOptions{
			QueueKey:   queueKey,
			ReadyLabel: label,
			Assignee:   assignee,
			Page:       page,
			PerPage:    perPage,
		})
		if err != nil {
			return nil, fmt.Errorf("search startrek queue %q: %w", queueKey, err)
		}

		for _, issue := range result.Issues {
			issueID := strings.TrimSpace(issue.ID)
			if issueID == "" {
				continue
			}
			issuesBySearchID[issueID] = issue
		}

		if result.TotalPages <= page || result.TotalPages <= 0 {
			break
		}
		page++
	}

	issueKeys := make([]string, 0, len(issuesBySearchID))
	for issueID := range issuesBySearchID {
		issueKeys = append(issueKeys, issueID)
	}
	sort.Strings(issueKeys)
	issues := make([]Issue, 0, len(issueKeys))
	for _, issueID := range issueKeys {
		issues = append(issues, issuesBySearchID[issueID])
	}

	issueIDs := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issueID := strings.TrimSpace(issue.ID); issueID != "" {
			issueIDs[issueID] = struct{}{}
		}
	}

	issuesByID := make(map[string]Issue, len(issues))
	for _, issue := range issues {
		task := MapIssueToTask(issue, nil, TaskMappingOptions{
			QueueKey: queueKey,
			RootID:   root.ID,
		})
		task.ID = strings.TrimSpace(task.ID)
		if task.ID == "" {
			continue
		}

		task.ParentID = startrekTreeParentID(issue, root.ID, issueIDs)
		tasks[task.ID] = task
		issuesByID[task.ID] = issue
		appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
			FromID: task.ParentID,
			ToID:   task.ID,
			Type:   contracts.RelationParent,
		})
	}

	for taskID, issue := range issuesByID {
		dependencyIDs := knownStartrekDependencyIDs(issue.DependencyIDs, tasks, taskID)
		if len(dependencyIDs) == 0 {
			continue
		}

		task := tasks[taskID]
		if task.Metadata == nil {
			task.Metadata = map[string]string{}
		}
		task.Metadata["dependencies"] = strings.Join(dependencyIDs, ",")
		tasks[taskID] = task

		for _, dependencyID := range dependencyIDs {
			appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: taskID,
				ToID:   dependencyID,
				Type:   contracts.RelationDependsOn,
			})
			appendUniqueStartrekRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: dependencyID,
				ToID:   taskID,
				Type:   contracts.RelationBlocks,
			})
		}
	}

	sort.Slice(relations, func(i, j int) bool {
		if relations[i].FromID != relations[j].FromID {
			return relations[i].FromID < relations[j].FromID
		}
		if relations[i].ToID != relations[j].ToID {
			return relations[i].ToID < relations[j].ToID
		}
		return relations[i].Type < relations[j].Type
	})

	return &contracts.TaskTree{
		Root:      root,
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

func (b *StorageBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}

	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, errors.New("startrek task ID is required")
	}

	issue, err := b.client.GetIssue(ctx, taskID)
	if err != nil {
		return nil, err
	}
	comments, err := b.client.GetIssueComments(ctx, taskID)
	if err != nil {
		return nil, err
	}

	queueKey := fallbackText(deriveQueueKey(issue.ID), deriveQueueKey(taskID))
	task := MapIssueToTask(issue, comments, TaskMappingOptions{
		QueueKey: queueKey,
		RootID:   queueKey,
	})
	return &task, nil
}

func (b *StorageBackend) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("startrek task ID is required")
	}
	return b.transitionIssueStatus(ctx, taskID, status)
}

func (b *StorageBackend) transitionIssueStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	transition, resolution, alternatives, err := b.statusTransition(status)
	if err != nil {
		return err
	}
	if transition == "" {
		return nil
	}
	if err := b.client.ExecuteIssueTransition(ctx, taskID, IssueTransitionOptions{
		Transition:             transition,
		AlternativeTransitions: alternatives,
		Resolution:             resolution,
	}); err != nil {
		if errors.Is(err, errStartrekNoMatchingTransition) {
			alreadySet, checkErr := b.issueAlreadyInTargetStatus(ctx, taskID, status)
			if checkErr != nil {
				return fmt.Errorf("transition startrek issue %q to task status %q: %w; check current startrek status: %v", taskID, status, err, checkErr)
			}
			if alreadySet {
				return nil
			}
		}
		return fmt.Errorf("transition startrek issue %q to task status %q: %w", taskID, status, err)
	}
	return nil
}

func (b *StorageBackend) issueAlreadyInTargetStatus(ctx context.Context, taskID string, status contracts.TaskStatus) (bool, error) {
	if b == nil || b.client == nil {
		return false, errors.New("startrek storage backend is not initialized")
	}
	issue, err := b.client.GetIssue(ctx, taskID)
	if err != nil {
		return false, err
	}
	// The native workflow status is the single source of truth for status.
	return workflowStatusMatchesTaskStatus(issue.Status, status), nil
}

func workflowStatusMatchesTaskStatus(workflowStatus string, status contracts.TaskStatus) bool {
	workflowStatus = strings.TrimSpace(workflowStatus)
	if workflowStatus == "" {
		return false
	}
	var keys []string
	switch status {
	case contracts.TaskStatusOpen:
		keys = []string{"open", "new", "reopened"}
	case contracts.TaskStatusInProgress:
		keys = []string{"inProgress", "in_progress"}
	case contracts.TaskStatusClosed:
		keys = []string{"closed", "resolved", "done"}
	case contracts.TaskStatusBlocked:
		keys = []string{"needInfo", "need_info", "blocked", "paused"}
	case contracts.TaskStatusFailed:
		keys = []string{"needInfo", "need_info", "failed"}
	}
	for _, key := range keys {
		if strings.EqualFold(workflowStatus, key) {
			return true
		}
	}
	return false
}

func (b *StorageBackend) statusTransition(status contracts.TaskStatus) (string, string, []string, error) {
	transitions := StatusTransitionNames{}
	if b != nil {
		transitions = b.statusTransitions
	}
	switch status {
	case contracts.TaskStatusOpen:
		return transitions.Ready, "", []string{"reopen", "open"}, nil
	case contracts.TaskStatusInProgress:
		return transitions.InProgress, "", []string{"start_progress", "inProgress", "startProgress", "start"}, nil
	case contracts.TaskStatusClosed:
		return transitions.Completed, transitions.CompletedResolution, []string{"close", "closed", "resolve", "done", "finish"}, nil
	case contracts.TaskStatusBlocked:
		return transitions.Blocked, "", []string{"need_info", "needInfo", "blocked", "pause"}, nil
	case contracts.TaskStatusFailed:
		return transitions.Failed, "", []string{"need_info", "needInfo", "failed"}, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported startrek task status %q", status)
	}
}

func (b *StorageBackend) SetTaskData(context.Context, string, map[string]string) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	return nil
}

func (b *StorageBackend) effectiveReadyLabel() string {
	if b == nil {
		return defaultStorageReadyLabel
	}
	return fallbackText(b.readyLabel, defaultStorageReadyLabel)
}

// taskStatusFromIssueStatus maps the native Startrek workflow status key to a
// contracts.TaskStatus. It is the inverse of workflowStatusMatchesTaskStatus
// and the single source of truth for read-path status derivation; the 4 status
// labels are gone.
func taskStatusFromIssueStatus(workflowStatus string) contracts.TaskStatus {
	workflowStatus = strings.TrimSpace(workflowStatus)
	if workflowStatus == "" {
		return contracts.TaskStatusOpen
	}
	switch {
	case equalsAnyKey(workflowStatus, "needInfo", "need_info", "failed"):
		return contracts.TaskStatusFailed
	case equalsAnyKey(workflowStatus, "blocked", "paused"):
		return contracts.TaskStatusBlocked
	case equalsAnyKey(workflowStatus, "closed", "resolved", "done"):
		return contracts.TaskStatusClosed
	case equalsAnyKey(workflowStatus, "inProgress", "in_progress"):
		return contracts.TaskStatusInProgress
	default:
		return contracts.TaskStatusOpen
	}
}

func equalsAnyKey(value string, keys ...string) bool {
	for _, key := range keys {
		if strings.EqualFold(value, key) {
			return true
		}
	}
	return false
}

func trimStatusTransitions(transitions StatusTransitionNames) StatusTransitionNames {
	return StatusTransitionNames{
		Ready:               strings.TrimSpace(transitions.Ready),
		InProgress:          strings.TrimSpace(transitions.InProgress),
		Completed:           strings.TrimSpace(transitions.Completed),
		Blocked:             strings.TrimSpace(transitions.Blocked),
		Failed:              strings.TrimSpace(transitions.Failed),
		CompletedResolution: strings.TrimSpace(transitions.CompletedResolution),
	}
}

func startrekTreeParentID(issue Issue, rootID string, issueIDs map[string]struct{}) string {
	parentID := strings.TrimSpace(issue.ParentID)
	if parentID != "" {
		if _, ok := issueIDs[parentID]; ok {
			return parentID
		}
	}
	return rootID
}

func knownStartrekDependencyIDs(dependencyIDs []string, tasks map[string]contracts.Task, taskID string) []string {
	if len(dependencyIDs) == 0 {
		return nil
	}

	known := make([]string, 0, len(dependencyIDs))
	seen := map[string]struct{}{}
	for _, dependencyID := range dependencyIDs {
		dependencyID = strings.TrimSpace(dependencyID)
		if dependencyID == "" || dependencyID == taskID {
			continue
		}
		if _, ok := tasks[dependencyID]; !ok {
			continue
		}
		if _, ok := seen[dependencyID]; ok {
			continue
		}
		seen[dependencyID] = struct{}{}
		known = append(known, dependencyID)
	}
	sort.Strings(known)
	return known
}

func hasStartrekLabel(labels []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), want) {
			return true
		}
	}
	return false
}

func appendUniqueStartrekRelation(relations *[]contracts.TaskRelation, seen map[string]struct{}, relation contracts.TaskRelation) {
	if relation.FromID == "" || relation.ToID == "" || relation.FromID == relation.ToID {
		return
	}
	key := string(relation.Type) + "|" + relation.FromID + "|" + relation.ToID
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*relations = append(*relations, relation)
}
