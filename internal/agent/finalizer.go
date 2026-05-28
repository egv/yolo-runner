package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/startrek"
)

const (
	parentSplitSubtaskIDsMetadataKey = "split_subtask_ids"
	parentPRCreatedMetadataKey       = "parent_pr_created"
	parentPRURLMetadataKey           = "parent_pr_url"
)

type parentFinalizer struct {
	mu        sync.Mutex
	tasks     contracts.TaskManager
	finalized map[string]struct{}
}

type parentSplitSubtaskIDReader interface {
	ParentSplitSubtaskIDs(ctx context.Context, parentID string) ([]string, bool, error)
}

type parentSplitSubtaskStatusReader interface {
	ParentSplitSubtaskStatuses(ctx context.Context, parentID string, subtaskIDs []string) (map[string]contracts.TaskStatus, bool, error)
}

type parentSplitParentIDReader interface {
	ParentSplitParentIDs(ctx context.Context, rootID string) ([]string, error)
}

type parentPRCreatedCommenter interface {
	PostParentPRCreated(ctx context.Context, parentID string, prURL string, subtaskIDs []string) error
}

type startrekIssueCommentCreator interface {
	CreateIssueComment(ctx context.Context, issueID string, opts startrek.IssueCommentCreateOptions) (startrek.IssueComment, error)
}

func newParentFinalizer(tasks contracts.TaskManager) *parentFinalizer {
	return &parentFinalizer{
		tasks:     tasks,
		finalized: map[string]struct{}{},
	}
}

func (l *Loop) finalizeParentIfReady(ctx context.Context) error {
	if l == nil || l.parentFinalizer == nil {
		return nil
	}
	before := l.parentPRURLSnapshot(ctx)
	created, err := l.parentFinalizer.FinalizeIfReady(ctx, l.options.ParentID, l.vcsForRepo(l.options.RepoRoot))
	if err != nil {
		return err
	}
	if created {
		l.emitParentPRCreatedEvents(ctx, before)
	}
	return nil
}

func (f *parentFinalizer) FinalizeIfReady(ctx context.Context, parentID string, vcs contracts.VCS) (bool, error) {
	parentID = strings.TrimSpace(parentID)
	if f == nil || f.tasks == nil || parentID == "" {
		return false, nil
	}
	prCreator, ok := vcs.(pullRequestCreator)
	if !ok || prCreator == nil {
		return false, nil
	}

	parentIDs, err := f.finalizationParentIDs(ctx, parentID)
	if err != nil {
		return false, err
	}
	createdAny := false
	for _, candidateParentID := range parentIDs {
		created, err := f.finalizeOneIfReady(ctx, candidateParentID, prCreator)
		if err != nil {
			return createdAny || created, err
		}
		createdAny = createdAny || created
	}
	return createdAny, nil
}

func (f *parentFinalizer) finalizationParentIDs(ctx context.Context, parentID string) ([]string, error) {
	parentIDs := []string{parentID}
	if reader, ok := f.tasks.(parentSplitParentIDReader); ok {
		splitParentIDs, err := reader.ParentSplitParentIDs(ctx, parentID)
		if err != nil {
			return nil, err
		}
		parentIDs = append(parentIDs, splitParentIDs...)
	}
	return uniqueNonEmptyIDs(parentIDs), nil
}

func (f *parentFinalizer) finalizeOneIfReady(ctx context.Context, parentID string, prCreator pullRequestCreator) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.finalized[parentID]; ok {
		return false, nil
	}

	parent, err := f.tasks.GetTask(ctx, parentID)
	if err != nil {
		return false, nil
	}
	if parentPRAlreadyCreated(parent.Metadata) {
		f.finalized[parentID] = struct{}{}
		return false, nil
	}

	subtaskIDs, err := f.splitSubtaskIDs(ctx, parentID, parent.Metadata)
	if err != nil {
		return false, err
	}
	if len(subtaskIDs) == 0 {
		return false, nil
	}
	closed, err := f.allSplitSubtasksClosed(ctx, parentID, subtaskIDs)
	if err != nil {
		return false, err
	}
	if !closed {
		return false, nil
	}

	prURL, err := prCreator.CreatePR(ctx, parentPRTitle(parent), parentPRBody(parent, subtaskIDs))
	if err != nil {
		return false, err
	}
	f.finalized[parentID] = struct{}{}

	data := map[string]string{parentPRCreatedMetadataKey: "true"}
	if prURL = strings.TrimSpace(prURL); prURL != "" {
		data[parentPRURLMetadataKey] = prURL
	}
	if err := f.tasks.SetTaskData(ctx, parentID, data); err != nil {
		return true, err
	}
	if commenter, ok := f.tasks.(parentPRCreatedCommenter); ok && strings.TrimSpace(prURL) != "" {
		if err := commenter.PostParentPRCreated(ctx, parentID, prURL, subtaskIDs); err != nil {
			return true, err
		}
	}
	return true, nil
}

func uniqueNonEmptyIDs(ids []string) []string {
	unique := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func (f *parentFinalizer) allSplitSubtasksClosed(ctx context.Context, parentID string, subtaskIDs []string) (bool, error) {
	if reader, ok := f.tasks.(parentSplitSubtaskStatusReader); ok {
		statuses, ok, err := reader.ParentSplitSubtaskStatuses(ctx, parentID, subtaskIDs)
		if err != nil {
			return false, err
		}
		if ok {
			for _, subtaskID := range subtaskIDs {
				if statuses[strings.TrimSpace(subtaskID)] != contracts.TaskStatusClosed {
					return false, nil
				}
			}
			return true, nil
		}
	}

	for _, subtaskID := range subtaskIDs {
		subtask, err := f.tasks.GetTask(ctx, subtaskID)
		if err != nil {
			return false, err
		}
		if subtask.Status != contracts.TaskStatusClosed {
			return false, nil
		}
	}
	return true, nil
}

func (f *parentFinalizer) splitSubtaskIDs(ctx context.Context, parentID string, metadata map[string]string) ([]string, error) {
	if subtaskIDs := splitSubtaskIDsFromMetadata(metadata); len(subtaskIDs) > 0 {
		return subtaskIDs, nil
	}
	reader, ok := f.tasks.(parentSplitSubtaskIDReader)
	if !ok {
		return nil, nil
	}
	subtaskIDs, ok, err := reader.ParentSplitSubtaskIDs(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return subtaskIDs, nil
}

func (m *storageEngineTaskManager) ParentSplitSubtaskIDs(ctx context.Context, parentID string) ([]string, bool, error) {
	if m == nil || m.storage == nil {
		return nil, false, nil
	}
	marker, ok, err := (startrek.SplitMarkerStore{Tracker: m.storage}).Read(ctx, parentID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return append([]string(nil), marker.SubtaskIDs...), true, nil
}

func (m *storageEngineTaskManager) PostParentPRCreated(ctx context.Context, parentID string, prURL string, subtaskIDs []string) error {
	if m == nil || m.storage == nil {
		return nil
	}
	commenter, ok := m.storage.(startrekIssueCommentCreator)
	if !ok || commenter == nil {
		return nil
	}
	return startrek.PostParentPRCreatedComment(ctx, commenter, parentID, prURL, subtaskIDs)
}

func (m *storageEngineTaskManager) ParentSplitSubtaskStatuses(_ context.Context, _ string, subtaskIDs []string) (map[string]contracts.TaskStatus, bool, error) {
	if m == nil || len(subtaskIDs) == 0 {
		return nil, false, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.graph == nil || len(m.graph.Nodes) == 0 {
		return nil, false, nil
	}

	statuses := make(map[string]contracts.TaskStatus, len(subtaskIDs))
	for _, subtaskID := range subtaskIDs {
		subtaskID = strings.TrimSpace(subtaskID)
		if subtaskID == "" {
			continue
		}
		node := m.graph.Nodes[subtaskID]
		if node == nil {
			return nil, false, nil
		}
		statuses[subtaskID] = node.Status
	}
	return statuses, true, nil
}

func (m *storageEngineTaskManager) ParentSplitParentIDs(ctx context.Context, rootID string) ([]string, error) {
	if m == nil {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rootID, err := m.resolveRootID(rootID)
	if err != nil {
		return nil, err
	}
	if m.graph == nil || strings.TrimSpace(m.graph.RootID) != rootID {
		if m.storage == nil || m.engine == nil {
			return nil, nil
		}
		if err := m.refreshGraphLocked(ctx, rootID); err != nil {
			return nil, err
		}
	}
	if m.graph == nil || len(m.graph.Nodes) == 0 {
		return nil, nil
	}

	parentIDs := make([]string, 0)
	for taskID, node := range m.graph.Nodes {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" || taskID == rootID || node == nil || len(node.Children) == 0 {
			continue
		}
		parentIDs = append(parentIDs, taskID)
	}
	sort.Strings(parentIDs)
	return parentIDs, nil
}

func parentPRAlreadyCreated(metadata map[string]string) bool {
	if strings.TrimSpace(metadata[parentPRURLMetadataKey]) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(metadata[parentPRCreatedMetadataKey]), "true")
}

func splitSubtaskIDsFromMetadata(metadata map[string]string) []string {
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

func parentPRTitle(parent contracts.Task) string {
	title := strings.TrimSpace(parent.Title)
	if title == "" {
		title = strings.TrimSpace(parent.ID)
	}
	if title == "" {
		return "Complete split task set"
	}
	return title
}

func parentPRBody(parent contracts.Task, subtaskIDs []string) string {
	var b strings.Builder
	parentID := strings.TrimSpace(parent.ID)
	if parentID != "" {
		fmt.Fprintf(&b, "Parent: %s\n\n", parentID)
	}
	description := strings.TrimSpace(parent.Description)
	if description != "" {
		fmt.Fprintf(&b, "%s\n\n", description)
	}
	b.WriteString("Completed split subtasks:")
	for _, subtaskID := range subtaskIDs {
		fmt.Fprintf(&b, "\n- %s", subtaskID)
	}
	return strings.TrimSpace(b.String())
}
