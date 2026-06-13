package startrek

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type Queue struct {
	Key string
}

type DiscoveryBackend interface {
	contracts.StorageBackend
	ResumeNeedsInfoTasks(ctx context.Context, input trackerstartrek.NeedsInfoResumeInput) ([]string, error)
}

func (s *Source) Poll(ctx context.Context) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("startrek source is required")
	}
	backend, err := s.discoveryBackend()
	if err != nil {
		return nil, err
	}
	preset := strings.TrimSpace(s.Preset)
	if preset == "" {
		return nil, errors.New("startrek source preset is required")
	}

	taskEngine := s.Engine
	if taskEngine == nil {
		taskEngine = engine.NewTaskEngine()
	}

	submissions := make([]workqueue.Submission, 0)
	for _, queue := range s.Queues {
		queueKey := strings.TrimSpace(queue.Key)
		if queueKey == "" {
			continue
		}

		resumedIDs, err := backend.ResumeNeedsInfoTasks(ctx, trackerstartrek.NeedsInfoResumeInput{
			QueueKey:       queueKey,
			ReadyLabel:     s.readyLabel(),
			NeedsInfoLabel: s.needsInfoLabel(),
			Marker:         s.marker(),
		})
		if err != nil {
			return nil, err
		}
		resumed := resumedIssueSet(resumedIDs)

		tree, err := backend.GetTaskTree(ctx, queueKey)
		if err != nil {
			return nil, err
		}
		graph, err := taskEngine.BuildGraph(tree)
		if err != nil {
			return nil, err
		}

		parentCache := map[string]contracts.Task{}
		for _, summary := range taskEngine.GetNextAvailable(graph) {
			if strings.EqualFold(strings.TrimSpace(summary.ID), strings.TrimSpace(tree.Root.ID)) {
				continue
			}
			task := TrackerWatchStartrekTaskFromTree(summary, tree.Tasks)
			hasOpenItem, err := s.hasOpenQueueItem(task.ID)
			if err != nil {
				return nil, err
			}
			if hasOpenItem {
				continue
			}

			queueRoot, err := s.preflightQueueRoot(ctx, backend, tree.Root, task, tree.Tasks, parentCache)
			if err != nil {
				return nil, err
			}
			task, err = s.preflightTaskDetails(ctx, backend, task)
			if err != nil {
				return nil, err
			}
			submission, err := s.preflightSubmission(ctx, task, queueRoot, summary.Priority, resumedIssue(resumed, task.ID))
			if err != nil {
				return nil, err
			}
			submissions = append(submissions, submission)
		}
	}
	return submissions, nil
}

func (s *Source) discoveryBackend() (DiscoveryBackend, error) {
	if s.Backend != nil {
		return s.Backend, nil
	}
	if backend, ok := s.Tracker.(DiscoveryBackend); ok {
		return backend, nil
	}
	return nil, errors.New("startrek source backend is required")
}

func (s *Source) hasOpenQueueItem(sourceRef string) (bool, error) {
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceRef == "" {
		return false, nil
	}
	if s == nil || s.Queue == nil {
		return false, errors.New("startrek source queue is required")
	}
	return s.Queue.HasOpenItem(s.Name(), sourceRef)
}

func (s *Source) preflightTaskDetails(ctx context.Context, backend DiscoveryBackend, task contracts.Task) (contracts.Task, error) {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return task, nil
	}
	detailed, err := backend.GetTask(ctx, taskID)
	if err != nil {
		return contracts.Task{}, fmt.Errorf("get startrek issue %q for preflight: %w", taskID, err)
	}
	if detailed == nil || strings.TrimSpace(detailed.ID) == "" {
		return task, nil
	}
	out := *detailed
	out.ParentID = task.ParentID
	out.Status = task.Status
	out.Metadata = mergeStartrekPreflightMetadata(task.Metadata, out.Metadata)
	return out, nil
}

func mergeStartrekPreflightMetadata(primary map[string]string, secondary map[string]string) map[string]string {
	if len(primary) == 0 && len(secondary) == 0 {
		return nil
	}
	merged := make(map[string]string, len(primary)+len(secondary))
	for key, value := range secondary {
		merged[key] = value
	}
	for key, value := range primary {
		merged[key] = value
	}
	return merged
}

func (s *Source) preflightQueueRoot(ctx context.Context, backend DiscoveryBackend, queueRoot contracts.Task, task contracts.Task, tasks map[string]contracts.Task, parentCache map[string]contracts.Task) (contracts.Task, error) {
	parentID := s.preflightParentID(queueRoot, task, tasks)
	if parentID == "" {
		return queueRoot, nil
	}
	if parentCache != nil {
		if cached, ok := parentCache[parentID]; ok {
			return cached, nil
		}
	}
	parent, err := backend.GetTask(ctx, parentID)
	if err != nil {
		return contracts.Task{}, fmt.Errorf("get startrek parent issue %q for preflight: %w", parentID, err)
	}
	if parent == nil || strings.TrimSpace(parent.ID) == "" {
		if parentCache != nil {
			parentCache[parentID] = queueRoot
		}
		return queueRoot, nil
	}
	if parentCache != nil {
		parentCache[parentID] = *parent
	}
	return *parent, nil
}

func (s *Source) preflightParentID(queueRoot contracts.Task, task contracts.Task, tasks map[string]contracts.Task) string {
	rootID := strings.TrimSpace(queueRoot.ID)
	parentID := strings.TrimSpace(task.ParentID)
	if parentID != "" && !strings.EqualFold(parentID, rootID) {
		return parentID
	}
	if !s.taskHasSubtaskLabel(task) {
		return ""
	}
	for _, dependencyID := range taskDependencyIDs(task) {
		if dependencyID == "" || strings.EqualFold(dependencyID, rootID) || strings.EqualFold(dependencyID, strings.TrimSpace(task.ID)) {
			continue
		}
		if dependencyTask, ok := tasks[dependencyID]; ok {
			dependencyParentID := strings.TrimSpace(dependencyTask.ParentID)
			if dependencyParentID != "" && !strings.EqualFold(dependencyParentID, rootID) {
				continue
			}
		}
		return dependencyID
	}
	return ""
}

func (s *Source) taskHasSubtaskLabel(task contracts.Task) bool {
	label := fallbackSourceText(s.SubtaskLabel, defaultSplitSubtaskLabel)
	for _, token := range taskDescriptionTokens(task) {
		if strings.EqualFold(strings.Trim(token, ".,;"), label) {
			return true
		}
	}
	return false
}

func taskDependencyIDs(task contracts.Task) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	for _, raw := range strings.Split(task.Metadata["dependencies"], ",") {
		ids = appendUniqueTaskDependencyID(ids, seen, raw)
	}
	for _, token := range taskDescriptionTokens(task) {
		dependencyID, ok := strings.CutPrefix(strings.TrimSpace(token), "depends-on:")
		if !ok {
			continue
		}
		ids = appendUniqueTaskDependencyID(ids, seen, dependencyID)
	}
	return ids
}

func taskDescriptionTokens(task contracts.Task) []string {
	return strings.FieldsFunc(task.Description, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func appendUniqueTaskDependencyID(ids []string, seen map[string]struct{}, raw string) []string {
	id := strings.Trim(strings.TrimSpace(raw), ".,;")
	if id == "" {
		return ids
	}
	key := strings.ToLower(id)
	if _, ok := seen[key]; ok {
		return ids
	}
	seen[key] = struct{}{}
	return append(ids, id)
}

func (s *Source) preflightSubmission(ctx context.Context, task contracts.Task, queueRoot contracts.Task, graphPriority *int, resumed bool) (workqueue.Submission, error) {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return workqueue.Submission{}, errors.New("startrek preflight task id is required")
	}
	payload, err := json.Marshal(workitem.PreflightPayload{
		Task:      workitem.TaskPayloadFromTask(task),
		QueueRoot: workitem.TaskPayloadFromTask(queueRoot),
	})
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("encode startrek preflight payload for issue %q: %w", taskID, err)
	}

	priority := s.Priority
	if graphPriority != nil {
		priority = *graphPriority
	}
	revision := sourceStartrekPreflightRevision(task)
	if resumed {
		revision, err = s.resumedPreflightRevision(ctx, taskID, revision)
		if err != nil {
			return workqueue.Submission{}, err
		}
	}
	return workqueue.Submission{
		Kind:           workitem.KindPreflight,
		Source:         s.Name(),
		SourceRef:      taskID,
		IdempotencyKey: "st/" + taskID + "/preflight/" + revision,
		Preset:         strings.TrimSpace(s.Preset),
		Priority:       priority,
		Payload:        payload,
		MaxAttempts:    s.MaxAttempts,
	}, nil
}

func (s *Source) resumedPreflightRevision(ctx context.Context, taskID string, revision string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	revision = fallbackSourceText(revision, "resume")
	for i := 1; i <= 100; i++ {
		candidate := safeStartrekKeyPart(revision + ":resume" + strconv.Itoa(i))
		if s == nil || s.State == nil {
			return candidate, nil
		}
		key := "st/" + strings.TrimSpace(taskID) + "/preflight/" + candidate
		if _, ok, err := s.State.GetPreflightWriteback(ctx, key); err != nil {
			return "", err
		} else if ok {
			continue
		}
		return candidate, nil
	}
	return safeStartrekKeyPart(revision + ":resume"), nil
}

func sourceStartrekPreflightRevision(task contracts.Task) string {
	if task.Metadata != nil {
		for _, key := range []string{"revision", "updated_at", "updatedAt", "updated"} {
			if value := safeStartrekKeyPart(task.Metadata[key]); value != "" {
				return value
			}
		}
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(task.ID))
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(task.Title))
	b.WriteByte('\n')
	b.WriteString(task.Description)
	b.WriteByte('\n')
	b.WriteString(strings.TrimSpace(task.ParentID))
	if len(task.Metadata) > 0 {
		keys := make([]string, 0, len(task.Metadata))
		for key := range task.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b.WriteByte('\n')
			b.WriteString(strings.TrimSpace(key))
			b.WriteByte('=')
			b.WriteString(strings.TrimSpace(task.Metadata[key]))
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func safeStartrekKeyPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func resumedIssueSet(issueIDs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(issueIDs))
	for _, issueID := range issueIDs {
		issueID = strings.TrimSpace(issueID)
		if issueID != "" {
			set[strings.ToLower(issueID)] = struct{}{}
		}
	}
	return set
}

func resumedIssue(set map[string]struct{}, issueID string) bool {
	_, ok := set[strings.ToLower(strings.TrimSpace(issueID))]
	return ok
}
