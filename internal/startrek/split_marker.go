package startrek

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const (
	defaultSplitMarkerVersion = "1"

	splitMarkerVersionKey    = "split_version"
	splitMarkerSubtaskIDsKey = "split_subtask_ids"
)

type SplitMarker struct {
	Version    string
	SubtaskIDs []string
}

type SplitMarkerTracker interface {
	GetTask(ctx context.Context, taskID string) (*contracts.Task, error)
	SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}

type SplitMarkerStore struct {
	Tracker      SplitMarkerTracker
	SplitVersion string
}

func (s SplitMarkerStore) Read(ctx context.Context, parentID string) (SplitMarker, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Tracker == nil {
		return SplitMarker{}, false, errors.New("startrek split marker tracker is required")
	}

	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return SplitMarker{}, false, errors.New("startrek parent issue id is required")
	}

	task, err := s.Tracker.GetTask(ctx, parentID)
	if err != nil {
		return SplitMarker{}, false, fmt.Errorf("read startrek split marker on issue %q: %w", parentID, err)
	}
	if task == nil || len(task.Metadata) == 0 {
		return SplitMarker{}, false, nil
	}

	version := strings.TrimSpace(task.Metadata[splitMarkerVersionKey])
	subtaskIDs := splitMarkerSubtaskIDs(task.Metadata[splitMarkerSubtaskIDsKey])
	if version == "" && len(subtaskIDs) == 0 {
		return SplitMarker{}, false, nil
	}
	if version == "" {
		return SplitMarker{}, false, fmt.Errorf("startrek split marker on issue %q is missing version", parentID)
	}
	if len(subtaskIDs) == 0 {
		return SplitMarker{}, false, fmt.Errorf("startrek split marker on issue %q has no subtask ids", parentID)
	}

	return SplitMarker{
		Version:    version,
		SubtaskIDs: subtaskIDs,
	}, true, nil
}

func (s SplitMarkerStore) Write(ctx context.Context, parentID string, marker SplitMarker) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Tracker == nil {
		return errors.New("startrek split marker tracker is required")
	}

	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return errors.New("startrek parent issue id is required")
	}

	version := fallbackText(marker.Version, s.effectiveSplitVersion())
	subtaskIDs := normalizedSplitMarkerSubtaskIDs(marker.SubtaskIDs)
	if len(subtaskIDs) == 0 {
		return errors.New("startrek split marker subtask ids are required")
	}

	task, err := s.Tracker.GetTask(ctx, parentID)
	if err != nil {
		return fmt.Errorf("read startrek task data before writing split marker on issue %q: %w", parentID, err)
	}
	data := map[string]string{}
	if task != nil {
		for key, value := range task.Metadata {
			data[key] = value
		}
	}

	data[splitMarkerVersionKey] = version
	data[splitMarkerSubtaskIDsKey] = strings.Join(subtaskIDs, ",")
	if err := s.Tracker.SetTaskData(ctx, parentID, data); err != nil {
		return fmt.Errorf("write startrek split marker on issue %q: %w", parentID, err)
	}
	return nil
}

func (s SplitMarkerStore) effectiveSplitVersion() string {
	return fallbackText(s.SplitVersion, defaultSplitMarkerVersion)
}

type IdempotentSplitSubtaskCreationTracker interface {
	SplitSubtaskCreationTracker
	SplitMarkerTracker
}

type IdempotentSplitSubtaskCreationService struct {
	Tracker      IdempotentSplitSubtaskCreationTracker
	ReadyLabel   string
	SubtaskLabel string
	SplitVersion string
}

func (s IdempotentSplitSubtaskCreationService) Create(ctx context.Context, input SplitSubtasksInput) (SplitSubtasksResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Tracker == nil {
		return SplitSubtasksResult{}, errors.New("startrek split subtask tracker is required")
	}

	parentID := strings.TrimSpace(input.ParentID)
	if parentID == "" {
		return SplitSubtasksResult{}, errors.New("startrek parent issue id is required")
	}

	tasks, err := orderedSplitSubtasks(input.Output.Tasks)
	if err != nil {
		return SplitSubtasksResult{}, err
	}
	if len(tasks) == 0 {
		return SplitSubtasksResult{}, errors.New("split output contains no tasks")
	}

	store := SplitMarkerStore{
		Tracker:      s.Tracker,
		SplitVersion: s.SplitVersion,
	}
	if marker, ok, err := store.Read(ctx, parentID); err != nil {
		return SplitSubtasksResult{}, err
	} else if ok {
		if marker.Version != store.effectiveSplitVersion() {
			return SplitSubtasksResult{}, fmt.Errorf("startrek split marker on issue %q has version %q, want %q", parentID, marker.Version, store.effectiveSplitVersion())
		}
		return splitSubtasksResultFromMarker(parentID, tasks, marker)
	}

	result, err := SplitSubtaskCreationService{
		Tracker:      s.Tracker,
		ReadyLabel:   s.ReadyLabel,
		SubtaskLabel: s.SubtaskLabel,
	}.Create(ctx, input)
	if err != nil {
		return SplitSubtasksResult{}, err
	}

	subtaskIDs, err := splitMarkerSubtaskIDsFromResult(tasks, result.IssueIDsBySplitTaskID)
	if err != nil {
		return SplitSubtasksResult{}, err
	}
	if err := store.Write(ctx, parentID, SplitMarker{
		Version:    store.effectiveSplitVersion(),
		SubtaskIDs: subtaskIDs,
	}); err != nil {
		return SplitSubtasksResult{}, err
	}

	return result, nil
}

func splitSubtasksResultFromMarker(parentID string, tasks []splitter.Task, marker SplitMarker) (SplitSubtasksResult, error) {
	subtaskIDs := normalizedSplitMarkerSubtaskIDs(marker.SubtaskIDs)
	if len(subtaskIDs) != len(tasks) {
		return SplitSubtasksResult{}, fmt.Errorf("startrek split marker has %d subtask ids for %d split tasks", len(subtaskIDs), len(tasks))
	}

	result := SplitSubtasksResult{
		Issues:                make([]Issue, 0, len(tasks)),
		IssueIDsBySplitTaskID: make(map[string]string, len(tasks)),
	}
	for i, task := range tasks {
		taskID := trimSplitRef(task.ID)
		issueID := subtaskIDs[i]
		result.IssueIDsBySplitTaskID[taskID] = issueID
		result.Issues = append(result.Issues, Issue{
			ID:       issueID,
			Title:    splitSubtaskTitle(task),
			ParentID: parentID,
		})
	}
	return result, nil
}

func splitMarkerSubtaskIDsFromResult(tasks []splitter.Task, issueIDsBySplitTaskID map[string]string) ([]string, error) {
	subtaskIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskID := trimSplitRef(task.ID)
		issueID := strings.TrimSpace(issueIDsBySplitTaskID[taskID])
		if issueID == "" {
			return nil, fmt.Errorf("split result is missing startrek issue id for split task %q", taskID)
		}
		subtaskIDs = append(subtaskIDs, issueID)
	}
	return subtaskIDs, nil
}

func splitMarkerSubtaskIDs(raw string) []string {
	return normalizedSplitMarkerSubtaskIDs(strings.Split(raw, ","))
}

func normalizedSplitMarkerSubtaskIDs(raw []string) []string {
	ids := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, id := range raw {
		id = strings.TrimSpace(id)
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
