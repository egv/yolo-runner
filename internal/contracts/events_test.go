package contracts_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestMarshalEventJSONLStableOrder(t *testing.T) {
	e := contracts.Event{
		Type:      contracts.EventTypeRunnerFinished,
		TaskID:    "task-42",
		Message:   "runner completed",
		Metadata:  map[string]string{"mode": "implement", "status": "completed"},
		Timestamp: time.Date(2026, 2, 9, 12, 30, 0, 0, time.UTC),
	}

	line, err := contracts.MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	expected := `{"type":"runner_finished","task_id":"task-42","message":"runner completed","metadata":{"mode":"implement","status":"completed"},"ts":"2026-02-09T12:30:00Z"}`
	if strings.TrimSpace(line) != expected {
		t.Fatalf("unexpected json line\nexpected: %s\nactual:   %s", expected, strings.TrimSpace(line))
	}
}

func TestMarshalEventJSONLAlwaysEndsWithNewline(t *testing.T) {
	line, err := contracts.MarshalEventJSONL(contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "t-1", Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("expected JSONL output to end with newline")
	}
}

func TestMarshalEventJSONLIncludesTaskTitleWhenPresent(t *testing.T) {
	e := contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-7",
		TaskTitle: "Improve readability",
		Timestamp: time.Date(2026, 2, 10, 13, 0, 0, 0, time.UTC),
	}

	line, err := contracts.MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := `{"type":"task_started","task_id":"task-7","task_title":"Improve readability","ts":"2026-02-10T13:00:00Z"}`
	if strings.TrimSpace(line) != expected {
		t.Fatalf("unexpected json line\nexpected: %s\nactual:   %s", expected, strings.TrimSpace(line))
	}
}

func TestMarshalEventJSONLIncludesParallelContextWhenPresent(t *testing.T) {
	e := contracts.Event{
		Type:      contracts.EventTypeRunnerStarted,
		TaskID:    "task-9",
		TaskTitle: "Parallel execution",
		WorkerID:  "worker-2",
		ClonePath: "/tmp/clones/task-9",
		QueuePos:  3,
		Timestamp: time.Date(2026, 2, 10, 13, 5, 0, 0, time.UTC),
	}

	line, err := contracts.MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := `{"type":"runner_started","task_id":"task-9","task_title":"Parallel execution","worker_id":"worker-2","clone_path":"/tmp/clones/task-9","queue_pos":3,"ts":"2026-02-10T13:05:00Z"}`
	if strings.TrimSpace(line) != expected {
		t.Fatalf("unexpected json line\nexpected: %s\nactual:   %s", expected, strings.TrimSpace(line))
	}
}

func TestMarshalEventJSONLIncludesRunStartedEventMetadata(t *testing.T) {
	e := contracts.Event{
		Type: contracts.EventTypeRunStarted,
		Metadata: map[string]string{
			"root_id":                "yr-2y0b",
			"concurrency":            "2",
			"model":                  "openai/gpt-5.3-codex",
			"runner_timeout":         "15m0s",
			"stream":                 "true",
			"verbose_stream":         "false",
			"stream_output_buffer":   "64",
			"stream_output_interval": "150ms",
		},
		Timestamp: time.Date(2026, 2, 10, 14, 0, 0, 0, time.UTC),
	}

	line, err := contracts.MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(line), `"type":"run_started"`) {
		t.Fatalf("expected run_started type, got %q", strings.TrimSpace(line))
	}
	if !strings.Contains(strings.TrimSpace(line), `"root_id":"yr-2y0b"`) {
		t.Fatalf("expected root_id metadata, got %q", strings.TrimSpace(line))
	}
}

func TestMonitorEventsCarrySplitCommitAndPRMetadata(t *testing.T) {
	lineage := map[string]string{
		"split_id":   "split-42",
		"subtask_id": "T34",
		"queue":      "VAY",
		"arc_root":   "/arcadia",
	}
	parentLineage := map[string]string{
		"split_id":          "split-42",
		"queue":             "VAY",
		"arc_root":          "/arcadia",
		"split_subtask_ids": "VAY-43,VAY-44",
	}
	mgr := newEventFakeTaskManager(
		contracts.Task{ID: "VAY-42", Title: "Parent issue", Status: contracts.TaskStatusOpen, ParentID: "VAY", Metadata: parentLineage},
		contracts.Task{ID: "VAY-43", Title: "First split task", Status: contracts.TaskStatusClosed, ParentID: "VAY-42", Metadata: lineage},
		contracts.Task{ID: "VAY-44", Title: "Emit monitor metadata", Status: contracts.TaskStatusOpen, ParentID: "VAY-42", Metadata: lineage},
	)
	runner := &eventFakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}}
	sink := &eventRecordingSink{}
	vcs := &eventPRVCS{}
	loop := agent.NewLoop(mgr, runner, sink, agent.LoopOptions{
		ParentID:       "VAY-42",
		RepoRoot:       "/arcadia",
		MaxRetries:     0,
		MergeOnSuccess: true,
		VCS:            vcs,
	})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed split subtask, got %#v", summary)
	}

	splitEvent, ok := eventByTypeAndTask(sink.events, contracts.EventTypeTaskStarted, "VAY-44")
	if !ok {
		t.Fatalf("expected task_started split event, got %#v", sink.events)
	}
	assertMetadata(t, splitEvent.Metadata, map[string]string{
		"parent_id":  "VAY-42",
		"split_id":   "split-42",
		"subtask_id": "T34",
		"queue":      "VAY",
		"arc_root":   "/arcadia",
	})

	commitEvent, ok := eventByTypeTaskAndMetadata(sink.events, contracts.EventTypeTaskDataUpdated, "VAY-44", "auto_commit_sha")
	if !ok {
		t.Fatalf("expected auto-commit task_data_updated event, got %#v", sink.events)
	}
	assertMetadata(t, commitEvent.Metadata, map[string]string{
		"parent_id":  "VAY-42",
		"split_id":   "split-42",
		"subtask_id": "T34",
		"queue":      "VAY",
		"arc_root":   "/arcadia",
	})

	prEvent, ok := eventByTypeTaskAndMetadataValue(sink.events, contracts.EventTypeTaskDataUpdated, "VAY-42", "pr_url", "https://arc.example.test/review/123")
	if !ok {
		t.Fatalf("expected parent PR task_data_updated event, got %#v", sink.events)
	}
	assertMetadata(t, prEvent.Metadata, map[string]string{
		"parent_id": "VAY",
		"split_id":  "split-42",
		"queue":     "VAY",
		"arc_root":  "/arcadia",
		"pr_url":    "https://arc.example.test/review/123",
	})
}

type eventFakeTaskManager struct {
	mu         sync.Mutex
	order      []string
	tasks      map[string]contracts.Task
	statusByID map[string]contracts.TaskStatus
	dataByID   map[string]map[string]string
}

func newEventFakeTaskManager(tasks ...contracts.Task) *eventFakeTaskManager {
	mgr := &eventFakeTaskManager{
		order:      make([]string, 0, len(tasks)),
		tasks:      map[string]contracts.Task{},
		statusByID: map[string]contracts.TaskStatus{},
		dataByID:   map[string]map[string]string{},
	}
	for _, task := range tasks {
		mgr.order = append(mgr.order, task.ID)
		mgr.tasks[task.ID] = task
		mgr.statusByID[task.ID] = task.Status
	}
	return mgr
}

func (m *eventFakeTaskManager) NextTasks(_ context.Context, parentID string) ([]contracts.TaskSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tasks := []contracts.TaskSummary{}
	for _, taskID := range m.order {
		task := m.tasks[taskID]
		if m.statusByID[taskID] != contracts.TaskStatusOpen || strings.TrimSpace(task.ParentID) != strings.TrimSpace(parentID) {
			continue
		}
		tasks = append(tasks, contracts.TaskSummary{ID: task.ID, Title: task.Title})
	}
	return tasks, nil
}

func (m *eventFakeTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, ok := m.tasks[taskID]
	if !ok {
		return contracts.Task{}, errors.New("missing task")
	}
	task.Status = m.statusByID[taskID]
	if len(task.Metadata) > 0 || len(m.dataByID[taskID]) > 0 {
		metadata := map[string]string{}
		for key, value := range task.Metadata {
			metadata[key] = value
		}
		for key, value := range m.dataByID[taskID] {
			metadata[key] = value
		}
		task.Metadata = metadata
	}
	return task, nil
}

func (m *eventFakeTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[taskID]; !ok {
		return errors.New("missing task")
	}
	m.statusByID[taskID] = status
	return nil
}

func (m *eventFakeTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[taskID]; !ok {
		return errors.New("missing task")
	}
	if m.dataByID[taskID] == nil {
		m.dataByID[taskID] = map[string]string{}
	}
	for key, value := range data {
		m.dataByID[taskID][key] = value
	}
	return nil
}

type eventFakeRunner struct {
	result contracts.RunnerResult
}

func (r *eventFakeRunner) Run(context.Context, contracts.RunnerRequest) (contracts.RunnerResult, error) {
	return r.result, nil
}

type eventRecordingSink struct {
	events []contracts.Event
}

func (s *eventRecordingSink) Emit(_ context.Context, event contracts.Event) error {
	s.events = append(s.events, event)
	return nil
}

type eventPRVCS struct {
	commitSHA string
}

func (v *eventPRVCS) EnsureMain(context.Context) error { return nil }

func (v *eventPRVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}

func (v *eventPRVCS) Checkout(context.Context, string) error { return nil }

func (v *eventPRVCS) CommitAll(context.Context, string) (string, error) {
	if v.commitSHA != "" {
		return v.commitSHA, nil
	}
	return "abc123", nil
}

func (v *eventPRVCS) MergeToMain(context.Context, string) error { return nil }

func (v *eventPRVCS) PushBranch(context.Context, string) error { return nil }

func (v *eventPRVCS) PushMain(context.Context) error { return nil }

func (v *eventPRVCS) CheckoutPRBranch(context.Context, string) (string, error) { return "", nil }
func (v *eventPRVCS) PushPRBranch(context.Context, string) error               { return nil }

func (v *eventPRVCS) CreatePR(context.Context, string, string) (string, error) {
	return "https://arc.example.test/review/123", nil
}

func eventByTypeAndTask(events []contracts.Event, eventType contracts.EventType, taskID string) (contracts.Event, bool) {
	for _, event := range events {
		if event.Type == eventType && event.TaskID == taskID {
			return event, true
		}
	}
	return contracts.Event{}, false
}

func eventByTypeTaskAndMetadata(events []contracts.Event, eventType contracts.EventType, taskID string, metadataKey string) (contracts.Event, bool) {
	for _, event := range events {
		if event.Type == eventType && event.TaskID == taskID && strings.TrimSpace(event.Metadata[metadataKey]) != "" {
			return event, true
		}
	}
	return contracts.Event{}, false
}

func eventByTypeTaskAndMetadataValue(events []contracts.Event, eventType contracts.EventType, taskID string, metadataKey string, metadataValue string) (contracts.Event, bool) {
	for _, event := range events {
		if event.Type == eventType && event.TaskID == taskID && event.Metadata[metadataKey] == metadataValue {
			return event, true
		}
	}
	return contracts.Event{}, false
}

func assertMetadata(t *testing.T, metadata map[string]string, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if metadata[key] != value {
			t.Fatalf("expected metadata[%q]=%q, got %q in %#v", key, value, metadata[key], metadata)
		}
	}
}
