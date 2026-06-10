package startrek

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const defaultStorageReadyLabel = "yolo-agent-ready"
const defaultStorageInProgressLabel = "yolo-agent-in-progress"
const defaultStorageCompletedLabel = "yolo-agent-completed"
const defaultStorageBlockedLabel = "yolo-agent-blocked"
const defaultStorageFailedLabel = "yolo-agent-failed"
const startrekSubtaskLabel = "agent:subtask"

// StorageBackend adapts Startrek issues to the storage-only contracts.StorageBackend API.
type StorageBackend struct {
	client            *Client
	readyLabel        string
	statusLabels      statusLabelNames
	statusTransitions StatusTransitionNames
	searchPerPage     int
	overrideMu        sync.RWMutex
	statusOverrides   map[string]contracts.TaskStatus
}

type statusLabelNames struct {
	Ready      string
	InProgress string
	Completed  string
	Blocked    string
	Failed     string
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
		statusOverrides:   map[string]contracts.TaskStatus{},
		statusLabels: statusLabelNames{
			Ready:      strings.TrimSpace(cfg.ReadyLabel),
			InProgress: strings.TrimSpace(cfg.InProgressLabel),
			Completed:  strings.TrimSpace(cfg.CompletedLabel),
			Blocked:    strings.TrimSpace(cfg.BlockedLabel),
			Failed:     strings.TrimSpace(cfg.FailedLabel),
		},
	}, nil
}

func (b *StorageBackend) GetTaskTree(ctx context.Context, queueKey string) (*contracts.TaskTree, error) {
	if b == nil || b.client == nil {
		return nil, errors.New("startrek storage backend is not initialized")
	}

	queueKey = strings.TrimSpace(queueKey)
	if queueKey == "" {
		return nil, errors.New("startrek queue key is required")
	}

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

	page := defaultIssueSearchPage
	perPage := b.searchPerPage
	if perPage <= 0 {
		perPage = defaultIssueSearchPerPage
	}

	issuesBySearchID := map[string]Issue{}
	for _, label := range b.searchStatusLabels() {
		page = defaultIssueSearchPage
		for {
			result, err := b.client.SearchIssues(ctx, IssueSearchOptions{
				QueueKey:   queueKey,
				ReadyLabel: label,
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
	}
	issueKeys := make([]string, 0, len(issuesBySearchID))
	statusOverrides := b.statusOverrideSnapshot()
	for issueID := range statusOverrides {
		if issueID == "" {
			continue
		}
		if !strings.EqualFold(deriveQueueKey(issueID), queueKey) {
			continue
		}
		if _, ok := issuesBySearchID[issueID]; ok {
			continue
		}
		issue, err := b.client.GetIssue(ctx, issueID)
		if err != nil {
			return nil, fmt.Errorf("get startrek issue %q with local status override: %w", issueID, err)
		}
		resolvedID := strings.TrimSpace(issue.ID)
		if resolvedID == "" {
			continue
		}
		if !strings.EqualFold(deriveQueueKey(resolvedID), queueKey) {
			continue
		}
		issuesBySearchID[resolvedID] = issue
	}

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
		task.Status = b.taskStatusFromLabels(issue.Labels)
		if status, ok := statusOverrides[task.ID]; ok {
			task.Status = status
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
	task.Status = b.taskStatusFromLabels(issue.Labels)
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
	addLabel, removeLabels, err := b.statusLabelTransition(status)
	if err != nil {
		return err
	}
	if err := b.transitionIssueStatus(ctx, taskID, status); err != nil {
		return err
	}
	for _, label := range removeLabels {
		if err := b.client.RemoveLabel(ctx, taskID, label); err != nil {
			return fmt.Errorf("remove startrek status label %q from issue %q: %w", label, taskID, err)
		}
	}
	if addLabel != "" {
		if err := b.client.AddLabel(ctx, taskID, addLabel); err != nil {
			return fmt.Errorf("add startrek status label %q to issue %q: %w", addLabel, taskID, err)
		}
	}
	b.recordStatusOverride(taskID, status)
	return nil
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
		return fmt.Errorf("transition startrek issue %q to task status %q: %w", taskID, status, err)
	}
	return nil
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

func (b *StorageBackend) effectiveStatusLabels() statusLabelNames {
	if b == nil {
		return statusLabelNames{
			Ready:      defaultStorageReadyLabel,
			InProgress: defaultStorageInProgressLabel,
			Completed:  defaultStorageCompletedLabel,
			Blocked:    defaultStorageBlockedLabel,
			Failed:     defaultStorageFailedLabel,
		}
	}
	return statusLabelNames{
		Ready:      fallbackText(b.statusLabels.Ready, defaultStorageReadyLabel),
		InProgress: fallbackText(b.statusLabels.InProgress, defaultStorageInProgressLabel),
		Completed:  fallbackText(b.statusLabels.Completed, defaultStorageCompletedLabel),
		Blocked:    fallbackText(b.statusLabels.Blocked, defaultStorageBlockedLabel),
		Failed:     fallbackText(b.statusLabels.Failed, defaultStorageFailedLabel),
	}
}

func (b *StorageBackend) searchStatusLabels() []string {
	labels := b.effectiveStatusLabels()
	return uniqueLabels([]string{labels.Ready, labels.InProgress, labels.Completed, labels.Blocked, labels.Failed})
}

func uniqueLabels(labels []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
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

func (b *StorageBackend) taskStatusFromLabels(issueLabels []string) contracts.TaskStatus {
	labels := b.effectiveStatusLabels()
	switch {
	case hasStartrekLabel(issueLabels, labels.Failed):
		return contracts.TaskStatusFailed
	case hasStartrekLabel(issueLabels, labels.Blocked):
		return contracts.TaskStatusBlocked
	case hasStartrekLabel(issueLabels, labels.Completed):
		return contracts.TaskStatusClosed
	case hasStartrekLabel(issueLabels, labels.InProgress):
		return contracts.TaskStatusInProgress
	default:
		return contracts.TaskStatusOpen
	}
}

func (b *StorageBackend) statusLabelTransition(status contracts.TaskStatus) (string, []string, error) {
	labels := b.effectiveStatusLabels()
	all := []string{labels.Ready, labels.InProgress, labels.Completed, labels.Blocked, labels.Failed}
	switch status {
	case contracts.TaskStatusOpen:
		return labels.Ready, labelsExcept(all, labels.Ready), nil
	case contracts.TaskStatusInProgress:
		return labels.InProgress, labelsExcept(all, labels.InProgress), nil
	case contracts.TaskStatusClosed:
		return labels.Completed, labelsExcept(all, labels.Completed), nil
	case contracts.TaskStatusBlocked:
		return labels.Blocked, labelsExcept(all, labels.Blocked), nil
	case contracts.TaskStatusFailed:
		return labels.Failed, labelsExcept(all, labels.Failed), nil
	default:
		return "", nil, fmt.Errorf("unsupported startrek task status %q", status)
	}
}

func (b *StorageBackend) taskStatusForLabel(label string) (contracts.TaskStatus, bool) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", false
	}
	labels := b.effectiveStatusLabels()
	switch {
	case strings.EqualFold(label, labels.Ready):
		return contracts.TaskStatusOpen, true
	case strings.EqualFold(label, labels.InProgress):
		return contracts.TaskStatusInProgress, true
	case strings.EqualFold(label, labels.Completed):
		return contracts.TaskStatusClosed, true
	case strings.EqualFold(label, labels.Blocked):
		return contracts.TaskStatusBlocked, true
	case strings.EqualFold(label, labels.Failed):
		return contracts.TaskStatusFailed, true
	default:
		return "", false
	}
}

func (b *StorageBackend) recordStatusOverride(taskID string, status contracts.TaskStatus) {
	if b == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || status == "" {
		return
	}
	b.overrideMu.Lock()
	defer b.overrideMu.Unlock()
	if b.statusOverrides == nil {
		b.statusOverrides = map[string]contracts.TaskStatus{}
	}
	b.statusOverrides[taskID] = status
}

func (b *StorageBackend) clearStatusOverrideIf(taskID string, status contracts.TaskStatus) {
	if b == nil {
		return
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || status == "" {
		return
	}
	b.overrideMu.Lock()
	defer b.overrideMu.Unlock()
	if b.statusOverrides == nil {
		return
	}
	if current, ok := b.statusOverrides[taskID]; ok && current == status {
		delete(b.statusOverrides, taskID)
	}
}

func (b *StorageBackend) statusOverrideSnapshot() map[string]contracts.TaskStatus {
	if b == nil {
		return nil
	}
	b.overrideMu.RLock()
	defer b.overrideMu.RUnlock()
	if len(b.statusOverrides) == 0 {
		return nil
	}
	out := make(map[string]contracts.TaskStatus, len(b.statusOverrides))
	for taskID, status := range b.statusOverrides {
		out[taskID] = status
	}
	return out
}

func labelsExcept(labels []string, keep string) []string {
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
	if hasStartrekLabel(issue.Labels, startrekSubtaskLabel) {
		parentID := strings.TrimSpace(issue.ParentID)
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
