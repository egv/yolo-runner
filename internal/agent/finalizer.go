package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/contracts"
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
	_, err := l.parentFinalizer.FinalizeIfReady(ctx, l.options.ParentID, l.vcsForRepo(l.options.RepoRoot))
	return err
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

	subtaskIDs := splitSubtaskIDsFromMetadata(parent.Metadata)
	if len(subtaskIDs) == 0 {
		return false, nil
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
	return true, nil
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
