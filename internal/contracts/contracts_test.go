package contracts

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestOutputEntryStructAndBufCap(t *testing.T) {
	entry := OutputEntry{
		Kind:    OutputEntryKindText,
		Content: "hello world",
	}
	if entry.Kind != OutputEntryKindText {
		t.Fatalf("expected Kind %q, got %q", OutputEntryKindText, entry.Kind)
	}
	if entry.Content != "hello world" {
		t.Fatalf("expected Content %q, got %q", "hello world", entry.Content)
	}
	if outputBufCap <= 0 {
		t.Fatalf("outputBufCap must be positive, got %d", outputBufCap)
	}
}

func TestOutputEntryKindTypeAndConstants(t *testing.T) {
	kinds := []OutputEntryKind{
		OutputEntryKindText,
		OutputEntryKindThinking,
		OutputEntryKindTool,
		OutputEntryKindSystem,
	}
	if len(kinds) != 4 {
		t.Fatalf("expected 4 OutputEntryKind constants, got %d", len(kinds))
	}
	seen := map[OutputEntryKind]struct{}{}
	for _, k := range kinds {
		if k == "" {
			t.Fatalf("OutputEntryKind constant must not be empty")
		}
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate OutputEntryKind constant %q", k)
		}
		seen[k] = struct{}{}
	}
}

func TestTaskStageTypeAndConstants(t *testing.T) {
	stages := []TaskStage{
		TaskStageIdle,
		TaskStageSelecting,
		TaskStageRunning,
		TaskStageCommitting,
		TaskStageClosing,
		TaskStageBlocked,
		TaskStageDone,
	}
	if len(stages) != 7 {
		t.Fatalf("expected 7 TaskStage constants, got %d", len(stages))
	}
	seen := map[TaskStage]struct{}{}
	for _, s := range stages {
		if s == "" {
			t.Fatalf("TaskStage constant must not be empty")
		}
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate TaskStage constant %q", s)
		}
		seen[s] = struct{}{}
	}
}

func TestTaskStatusConstants(t *testing.T) {
	expected := []TaskStatus{TaskStatusOpen, TaskStatusInProgress, TaskStatusBlocked, TaskStatusClosed, TaskStatusFailed}
	for _, status := range expected {
		if status == "" {
			t.Fatalf("status constant must not be empty")
		}
	}
}

func TestRunnerResultValidate(t *testing.T) {
	valid := RunnerResult{Status: RunnerResultCompleted}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected completed to be valid: %v", err)
	}

	invalid := RunnerResult{Status: RunnerResultStatus("unknown")}
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected unknown status to fail validation")
	}
}

func TestLoopSummaryCounts(t *testing.T) {
	summary := LoopSummary{Completed: 1, Blocked: 2, Failed: 3, Skipped: 4}
	if summary.TotalProcessed() != 10 {
		t.Fatalf("expected total processed to be 10, got %d", summary.TotalProcessed())
	}
}

func TestContractInterfacesCanBeImplementedByFakes(t *testing.T) {
	ctx := context.Background()
	manager := fakeTaskManager{}
	runner := fakeAgentRunner{}
	vcs := fakeVCS{}

	tasks, err := manager.NextTasks(ctx, "root")
	if err != nil {
		t.Fatalf("next tasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t-1" {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}

	result, err := runner.Run(ctx, RunnerRequest{TaskID: "t-1", Mode: RunnerModeImplement})
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}
	if result.Status != RunnerResultCompleted {
		t.Fatalf("unexpected runner status: %s", result.Status)
	}

	branch, err := vcs.CreateTaskBranch(ctx, "t-1")
	if err != nil {
		t.Fatalf("create task branch failed: %v", err)
	}
	if branch == "" {
		t.Fatalf("expected non-empty branch")
	}
}

type fakeTaskManager struct{}

func (fakeTaskManager) NextTasks(context.Context, string) ([]TaskSummary, error) {
	return []TaskSummary{{ID: "t-1", Title: "test"}}, nil
}

func (fakeTaskManager) GetTask(context.Context, string) (Task, error) {
	return Task{ID: "t-1", Title: "test"}, nil
}

func (fakeTaskManager) SetTaskStatus(context.Context, string, TaskStatus) error {
	return nil
}

func (fakeTaskManager) SetTaskData(context.Context, string, map[string]string) error {
	return nil
}

type fakeAgentRunner struct{}

func (fakeAgentRunner) Run(context.Context, RunnerRequest) (RunnerResult, error) {
	return RunnerResult{Status: RunnerResultCompleted}, nil
}

type fakeVCS struct{}

func (fakeVCS) EnsureMain(context.Context) error { return nil }

func (fakeVCS) CreateTaskBranch(context.Context, string) (string, error) { return "task/t-1", nil }

func (fakeVCS) Checkout(context.Context, string) error { return nil }

func (fakeVCS) CommitAll(context.Context, string) (string, error) { return "abc123", nil }

func (fakeVCS) MergeToMain(context.Context, string) error { return nil }

func (fakeVCS) PushBranch(context.Context, string) error { return nil }

func (fakeVCS) PushMain(context.Context) error { return nil }

func TestEventDefaults(t *testing.T) {
	event := Event{Type: EventTypeTaskStarted, TaskID: "t-1", Timestamp: time.Now().UTC()}
	if event.Type == "" || event.TaskID == "" || event.Timestamp.IsZero() {
		t.Fatalf("event fields should be populated")
	}
}

func TestRunnerResultRequiresStatus(t *testing.T) {
	err := (RunnerResult{}).Validate()
	if !errors.Is(err, ErrInvalidRunnerResultStatus) {
		t.Fatalf("expected ErrInvalidRunnerResultStatus, got %v", err)
	}
}

func TestTaskSessionRuntimeContractsCanBeImplementedByFakes(t *testing.T) {
	ctx := context.Background()
	runtime := fakeTaskSessionRuntime{}
	events := []TaskSessionEvent{}

	session, err := runtime.Start(ctx, TaskSessionStartRequest{
		TaskID:   "task-1",
		Backend:  "codex",
		RepoRoot: "/tmp/repo",
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if session.ID() != "session-task-1" {
		t.Fatalf("unexpected session id %q", session.ID())
	}
	if err := session.WaitReady(ctx); err != nil {
		t.Fatalf("wait ready: %v", err)
	}

	approvalCalls := 0
	questionCalls := 0
	err = session.Execute(ctx, TaskSessionExecuteRequest{
		Prompt: "ship it",
		ApprovalHandler: TaskSessionApprovalHandlerFunc(func(_ context.Context, req TaskSessionApprovalRequest) (TaskSessionApprovalDecision, error) {
			approvalCalls++
			if req.Kind != TaskSessionApprovalKindToolCall {
				t.Fatalf("unexpected approval kind %q", req.Kind)
			}
			return TaskSessionApprovalDecision{Outcome: TaskSessionApprovalApproved}, nil
		}),
		QuestionHandler: TaskSessionQuestionHandlerFunc(func(_ context.Context, req TaskSessionQuestionRequest) (TaskSessionQuestionResponse, error) {
			questionCalls++
			if req.ID != "q-1" {
				t.Fatalf("unexpected question id %q", req.ID)
			}
			return TaskSessionQuestionResponse{Answer: "decide yourself"}, nil
		}),
		EventSink: TaskSessionEventSinkFunc(func(_ context.Context, event TaskSessionEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("execute session: %v", err)
	}
	if approvalCalls != 1 {
		t.Fatalf("expected one approval call, got %d", approvalCalls)
	}
	if questionCalls != 1 {
		t.Fatalf("expected one question call, got %d", questionCalls)
	}
	if len(events) != 4 {
		t.Fatalf("expected four session events, got %#v", events)
	}

	if err := session.Cancel(ctx, TaskSessionCancellation{Reason: "user canceled"}); err != nil {
		t.Fatalf("cancel session: %v", err)
	}
	if err := session.Teardown(ctx, TaskSessionTeardown{Reason: "finished"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}
}

func TestNormalizeTaskSessionEventMapsCommonRuntimeSignals(t *testing.T) {
	timestamp := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)

	progress, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeLifecycle,
		Message:   "session ready",
		Timestamp: timestamp,
		Metadata:  map[string]string{"state": string(TaskSessionLifecycleReady)},
	})
	if !ok {
		t.Fatalf("expected lifecycle event to normalize")
	}
	if progress.Type != string(EventTypeRunnerProgress) {
		t.Fatalf("expected runner_progress, got %q", progress.Type)
	}
	if progress.Metadata["state"] != string(TaskSessionLifecycleReady) {
		t.Fatalf("expected lifecycle state metadata, got %#v", progress.Metadata)
	}

	output, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeOutput,
		Message:   "stderr: compiling",
		Timestamp: timestamp,
		Metadata:  map[string]string{"stream": "stderr"},
	})
	if !ok {
		t.Fatalf("expected output event to normalize")
	}
	if output.Type != string(EventTypeRunnerOutput) {
		t.Fatalf("expected runner_output, got %q", output.Type)
	}

	warning, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeApprovalRequired,
		Message:   "approval needed",
		Timestamp: timestamp,
		Metadata:  map[string]string{"kind": string(TaskSessionApprovalKindToolCall)},
	})
	if !ok {
		t.Fatalf("expected approval event to normalize")
	}
	if warning.Type != string(EventTypeRunnerWarning) {
		t.Fatalf("expected runner_warning, got %q", warning.Type)
	}

	artifact, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeArtifact,
		Message:   "coverage report",
		Timestamp: timestamp,
		Metadata:  map[string]string{"path": "/tmp/coverage.txt"},
	})
	if !ok {
		t.Fatalf("expected artifact event to normalize")
	}
	if artifact.Type != string(EventTypeRunnerProgress) {
		t.Fatalf("expected artifact to map to runner_progress, got %q", artifact.Type)
	}
	if !reflect.DeepEqual(artifact.Metadata, map[string]string{"path": "/tmp/coverage.txt", "sequence": "0"}) {
		t.Fatalf("unexpected artifact metadata %#v", artifact.Metadata)
	}
}

func TestNormalizeTaskSessionEventPreservesStructuredRuntimeFields(t *testing.T) {
	timestamp := time.Date(2026, 3, 18, 12, 5, 0, 0, time.UTC)

	progress, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeProgress,
		Message:   "indexing files",
		Timestamp: timestamp,
		Progress: &TaskSessionProgressEvent{
			Phase:   "indexing",
			Current: 2,
			Total:   5,
		},
	})
	if !ok {
		t.Fatalf("expected progress event to normalize")
	}
	if progress.Metadata["phase"] != "indexing" {
		t.Fatalf("expected phase metadata, got %#v", progress.Metadata)
	}
	if progress.Metadata["current"] != "2" || progress.Metadata["total"] != "5" {
		t.Fatalf("expected progress counters, got %#v", progress.Metadata)
	}

	cancellation, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeCancellation,
		Message:   "cancel requested",
		Timestamp: timestamp,
		Cancellation: &TaskSessionCancellationEvent{
			Reason: "user canceled",
			Force:  true,
		},
	})
	if !ok {
		t.Fatalf("expected cancellation event to normalize")
	}
	if cancellation.Metadata["reason"] != "user canceled" {
		t.Fatalf("expected cancellation reason metadata, got %#v", cancellation.Metadata)
	}
	if cancellation.Metadata["force"] != "true" {
		t.Fatalf("expected cancellation force metadata, got %#v", cancellation.Metadata)
	}

	teardown, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeTeardown,
		Message:   "session cleanup",
		Timestamp: timestamp,
		Teardown: &TaskSessionTeardownEvent{
			Reason: "runner finished",
			Force:  true,
		},
	})
	if !ok {
		t.Fatalf("expected teardown event to normalize")
	}
	if teardown.Metadata["reason"] != "runner finished" {
		t.Fatalf("expected teardown reason metadata, got %#v", teardown.Metadata)
	}
	if teardown.Metadata["force"] != "true" {
		t.Fatalf("expected teardown force metadata, got %#v", teardown.Metadata)
	}
}

func TestNormalizeTaskSessionEventPreservesCorrelationMetadata(t *testing.T) {
	timestamp := time.Date(2026, 3, 18, 12, 15, 0, 0, time.UTC)

	progress, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeApprovalRequired,
		Message:   "approval needed",
		Timestamp: timestamp,
		SessionID: "sess-123",
		Sequence:  42,
		Metadata: map[string]string{
			"thread_id": "thread-9",
			"turn_id":   "turn-4",
		},
		Approval: &TaskSessionApprovalEvent{
			Request: TaskSessionApprovalRequest{
				ID:   "approval-7",
				Kind: TaskSessionApprovalKindToolCall,
			},
		},
	})
	if !ok {
		t.Fatalf("expected approval event to normalize")
	}

	if progress.Metadata["session_id"] != "sess-123" {
		t.Fatalf("expected session_id metadata, got %#v", progress.Metadata)
	}
	if progress.Metadata["sequence"] != strconv.FormatInt(42, 10) {
		t.Fatalf("expected sequence metadata, got %#v", progress.Metadata)
	}
	if progress.Metadata["thread_id"] != "thread-9" || progress.Metadata["turn_id"] != "turn-4" {
		t.Fatalf("expected stable correlation ids, got %#v", progress.Metadata)
	}
	if progress.Metadata["approval_id"] != "approval-7" {
		t.Fatalf("expected approval_id metadata, got %#v", progress.Metadata)
	}
}

func TestNormalizeTaskSessionEventPreservesZeroValuedStructuredFields(t *testing.T) {
	timestamp := time.Date(2026, 3, 18, 12, 20, 0, 0, time.UTC)

	progress, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeProgress,
		Message:   "waiting for first item",
		Timestamp: timestamp,
		SessionID: "sess-zero",
		Sequence:  0,
		Progress: &TaskSessionProgressEvent{
			Phase:   "queued",
			Current: 0,
			Total:   0,
		},
	})
	if !ok {
		t.Fatalf("expected progress event to normalize")
	}
	if progress.Metadata["session_id"] != "sess-zero" {
		t.Fatalf("expected session_id metadata, got %#v", progress.Metadata)
	}
	if progress.Metadata["sequence"] != "0" {
		t.Fatalf("expected zero sequence metadata, got %#v", progress.Metadata)
	}
	if progress.Metadata["current"] != "0" || progress.Metadata["total"] != "0" {
		t.Fatalf("expected zero progress counters, got %#v", progress.Metadata)
	}
}

func TestNormalizeTaskSessionEventPreservesZeroSequenceWithoutSessionID(t *testing.T) {
	timestamp := time.Date(2026, 3, 18, 12, 25, 0, 0, time.UTC)

	progress, ok := NormalizeTaskSessionEvent(TaskSessionEvent{
		Type:      TaskSessionEventTypeProgress,
		Message:   "queued for execution",
		Timestamp: timestamp,
		Sequence:  0,
		Progress: &TaskSessionProgressEvent{
			Phase: "queued",
		},
	})
	if !ok {
		t.Fatalf("expected progress event to normalize")
	}
	if _, exists := progress.Metadata["session_id"]; exists {
		t.Fatalf("expected empty session_id to be omitted, got %#v", progress.Metadata)
	}
	if progress.Metadata["sequence"] != "0" {
		t.Fatalf("expected zero sequence metadata without session id, got %#v", progress.Metadata)
	}
}

func TestTaskSessionApprovalAndQuestionContractsAllowStructuredPayloads(t *testing.T) {
	approval := TaskSessionApprovalEvent{
		Request: TaskSessionApprovalRequest{
			ID:      "approval-1",
			Title:   "Apply patch",
			Message: "Need approval",
			Payload: map[string]any{
				"raw_input": map[string]any{
					"command": []any{"git", "apply"},
				},
				"locations": []any{
					map[string]any{"path": "README.md", "line": float64(12)},
				},
				"diff": "@@ -1 +1 @@",
			},
		},
		Decision: &TaskSessionApprovalDecision{
			Outcome: TaskSessionApprovalApproved,
			Payload: map[string]any{
				"raw_output": map[string]any{"approved": true},
			},
		},
	}
	question := TaskSessionQuestionEvent{
		Request: TaskSessionQuestionRequest{
			ID:     "question-1",
			Prompt: "Continue?",
			Payload: map[string]any{
				"resource": map[string]any{
					"path":    "docs/spec.md",
					"content": "body",
				},
				"options": []any{"yes", "no"},
			},
		},
		Response: &TaskSessionQuestionResponse{
			Answer: "yes",
			Payload: map[string]any{
				"raw_output": map[string]any{"answer": "yes"},
			},
		},
	}

	approvalLocations, ok := approval.Request.Payload.(map[string]any)["locations"].([]any)
	if !ok || len(approvalLocations) != 1 {
		t.Fatalf("expected structured approval payload, got %#v", approval.Request.Payload)
	}
	questionResource, ok := question.Request.Payload.(map[string]any)["resource"].(map[string]any)
	if !ok || questionResource["path"] != "docs/spec.md" {
		t.Fatalf("expected structured question payload, got %#v", question.Request.Payload)
	}
	if got := approval.Decision.Payload.(map[string]any)["raw_output"].(map[string]any)["approved"]; got != true {
		t.Fatalf("expected structured approval decision payload, got %#v", approval.Decision.Payload)
	}
	if got := question.Response.Payload.(map[string]any)["raw_output"].(map[string]any)["answer"]; got != "yes" {
		t.Fatalf("expected structured question response payload, got %#v", question.Response.Payload)
	}
}

type fakeTaskSessionRuntime struct{}

func (fakeTaskSessionRuntime) Start(context.Context, TaskSessionStartRequest) (TaskSession, error) {
	return &fakeTaskSession{id: "session-task-1"}, nil
}

type fakeTaskSession struct {
	id string
}

func (s *fakeTaskSession) ID() string { return s.id }

func (s *fakeTaskSession) WaitReady(context.Context) error { return nil }

func (s *fakeTaskSession) Execute(ctx context.Context, req TaskSessionExecuteRequest) error {
	if req.ApprovalHandler != nil {
		if _, err := req.ApprovalHandler.HandleApproval(ctx, TaskSessionApprovalRequest{
			ID:   "a-1",
			Kind: TaskSessionApprovalKindToolCall,
		}); err != nil {
			return err
		}
	}
	if req.QuestionHandler != nil {
		if _, err := req.QuestionHandler.HandleQuestion(ctx, TaskSessionQuestionRequest{
			ID:      "q-1",
			Prompt:  "Need a decision",
			Context: "tool_call",
		}); err != nil {
			return err
		}
	}
	if req.EventSink != nil {
		for _, event := range []TaskSessionEvent{
			{Type: TaskSessionEventTypeLifecycle, Message: "starting", Timestamp: time.Now().UTC()},
			{Type: TaskSessionEventTypeApprovalRequired, Message: "approval", Timestamp: time.Now().UTC()},
			{Type: TaskSessionEventTypeQuestionAsked, Message: "question", Timestamp: time.Now().UTC()},
			{Type: TaskSessionEventTypeArtifact, Message: "artifact", Timestamp: time.Now().UTC()},
		} {
			if err := req.EventSink.HandleEvent(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *fakeTaskSession) Cancel(context.Context, TaskSessionCancellation) error { return nil }

func (s *fakeTaskSession) Teardown(context.Context, TaskSessionTeardown) error { return nil }
