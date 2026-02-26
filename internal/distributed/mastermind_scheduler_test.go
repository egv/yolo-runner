package distributed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func registerExecutorWithCapabilities(t *testing.T, ctx context.Context, bus Bus, subjects EventSubjects, executorID string, availableSlots int, capabilities ...Capability) {
	t.Helper()
	registration := ExecutorRegistrationPayload{
		ExecutorID:     executorID,
		Capabilities:   capabilities,
		MaxConcurrency: availableSlots,
	}
	regEnv, err := NewEventEnvelope(EventTypeExecutorRegistered, "exec", "", registration)
	if err != nil {
		t.Fatalf("new registration envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.Register, regEnv); err != nil {
		t.Fatalf("publish registration: %v", err)
	}
}

func TestMastermindSchedulerE2EEnqueueClaimCompleteAndAck(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("sched-e2e")
	backend := &fakeTaskStatusBackend{t: t}
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	ackCh, unsubscribeAck, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateAck)
	if err != nil {
		t.Fatalf("subscribe ack: %v", err)
	}
	defer unsubscribeAck()

	runner := &runtimeTestRunner{}
	runner.runFn = func(_ context.Context, _ contracts.RunnerRequest, _ int) (contracts.RunnerResult, error) {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	worker := NewQueueWorkerRuntime(QueueWorkerRuntimeOptions{
		ID:                "worker-1",
		Bus:               bus,
		Runner:            runner,
		Queue:             "queue.tasks.implement",
		QueueGroup:        "workers",
		Subjects:          subjects,
		Capabilities:      []Capability{CapabilityImplement},
		HeartbeatInterval: 20 * time.Millisecond,
		CapabilityProbe: func(context.Context) QueueWorkerCapabilitySnapshot {
			return QueueWorkerCapabilitySnapshot{CredentialFlags: map[string]bool{"has_env:GITHUB_TOKEN": true}}
		},
	})
	go func() { _ = worker.Start(ctx) }()

	time.Sleep(60 * time.Millisecond)
	snapshot := TaskGraphSnapshotPayload{
		SchemaVersion: InboxSchemaVersionV1,
		Graphs: []TaskGraphSnapshot{{
			GraphRef: "run-159",
			Nodes: []TaskGraphNode{{
				TaskID:   "101",
				Status:   contracts.TaskStatusOpen,
				GraphRef: "run-159",
				TaskRef: TaskRef{
					BackendType:     "tk",
					BackendNativeID: "101",
				},
				Requirements: []TaskRequirement{
					{Name: "implement", Kind: "capability"},
					{Name: "has_env:GITHUB_TOKEN", Kind: "credential_flag"},
				},
			}},
		}},
	}
	env, err := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	if err != nil {
		t.Fatalf("new snapshot envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskGraphSnapshot, env); err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}

	ack := readTaskStatusUpdateAck(t, ackCh)
	if !ack.Success {
		t.Fatalf("expected success ack, got %#v", ack)
	}

	waitDeadline := time.After(2 * time.Second)
	for {
		status, _ := backend.status("101")
		if status == contracts.TaskStatusClosed {
			break
		}
		select {
		case <-waitDeadline:
			t.Fatalf("timed out waiting for status closed; current=%q", status)
		case <-time.After(25 * time.Millisecond):
		}
	}

	if runner.Attempts() == 0 {
		t.Fatalf("expected worker to claim queued task")
	}
	_, data := backend.status("101")
	if !strings.Contains(strings.ToLower(data[inboxStatusCommentKey]), "completed") {
		t.Fatalf("expected completion comment, got %#v", data)
	}
}

func TestMastermindSchedulerFairnessAcrossGraphs(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-fair")
	mastermind := NewMastermind(MastermindOptions{ID: "m", Bus: bus, Subjects: subjects, RegistryTTL: 2 * time.Second, RequestTimeout: 2 * time.Second})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	registerExecutorWithCapabilities(t, ctx, bus, subjects, "exec-1", 2, CapabilityImplement)
	hbEnv, _ := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "", ExecutorHeartbeatPayload{ExecutorID: "exec-1", AvailableSlots: 2, MaxConcurrency: 2})
	if err := bus.Publish(ctx, subjects.Heartbeat, hbEnv); err != nil {
		t.Fatalf("publish heartbeat: %v", err)
	}

	snapshot := TaskGraphSnapshotPayload{SchemaVersion: InboxSchemaVersionV1, Graphs: []TaskGraphSnapshot{
		{GraphRef: "g-a", Nodes: []TaskGraphNode{
			{TaskID: "a-1", GraphRef: "g-a", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "a-1"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
			{TaskID: "a-2", GraphRef: "g-a", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "a-2"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
		}},
		{GraphRef: "g-b", Nodes: []TaskGraphNode{
			{TaskID: "b-1", GraphRef: "g-b", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "b-1"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
			{TaskID: "b-2", GraphRef: "g-b", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "b-2"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
		}},
	}}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	if err := bus.Publish(ctx, subjects.TaskGraphSnapshot, env); err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}

	msgs, stop, err := bus.ConsumeQueue(ctx, "queue.tasks.implement", QueueConsumeOptions{Consumer: "probe"})
	if err != nil {
		t.Fatalf("consume queue: %v", err)
	}
	defer stop()
	seenGraphs := map[string]bool{}
	for len(seenGraphs) < 2 {
		select {
		case msg := <-msgs:
			payload := QueueTaskMessage{}
			if err := json.Unmarshal(msg.Event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal queue payload: %v", err)
			}
			seenGraphs[payload.GraphRef] = true
			_ = msg.Ack(ctx)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for fair enqueue across graphs; seen=%v", seenGraphs)
		}
	}
}

func TestMastermindSchedulerBackpressureLimitsQueuedTasks(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-bp")
	mastermind := NewMastermind(MastermindOptions{ID: "m", Bus: bus, Subjects: subjects, RegistryTTL: 2 * time.Second, RequestTimeout: 2 * time.Second})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	registerExecutorWithCapabilities(t, ctx, bus, subjects, "exec-1", 1, CapabilityImplement)
	hbEnv, _ := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "", ExecutorHeartbeatPayload{ExecutorID: "exec-1", AvailableSlots: 1, MaxConcurrency: 1})
	_ = bus.Publish(ctx, subjects.Heartbeat, hbEnv)

	snapshot := TaskGraphSnapshotPayload{SchemaVersion: InboxSchemaVersionV1, Graphs: []TaskGraphSnapshot{{GraphRef: "g", Nodes: []TaskGraphNode{
		{TaskID: "1", GraphRef: "g", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "1"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
		{TaskID: "2", GraphRef: "g", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "2"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
		{TaskID: "3", GraphRef: "g", Status: contracts.TaskStatusOpen, TaskRef: TaskRef{BackendType: "tk", BackendNativeID: "3"}, Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}}},
	}}}}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)

	msgs, stop, err := bus.ConsumeQueue(ctx, "queue.tasks.implement", QueueConsumeOptions{Consumer: "probe"})
	if err != nil {
		t.Fatalf("consume queue: %v", err)
	}
	defer stop()
	count := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case msg := <-msgs:
			count++
			_ = msg.Ack(ctx)
		case <-timeout:
			break loop
		}
	}
	if count != 1 {
		t.Fatalf("expected backpressure to cap enqueues at 1, got %d", count)
	}
}

func TestMastermindSchedulerBlocksTaskWhenNoExecutorMatches(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-blocked")
	backend := &fakeTaskStatusBackend{t: t}
	mastermind := NewMastermind(MastermindOptions{
		ID:             "m",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"github": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	registerExecutorWithCapabilities(t, ctx, bus, subjects, "exec-1", 1, CapabilityImplement)

	hbEnv, _ := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "", ExecutorHeartbeatPayload{ExecutorID: "exec-1", AvailableSlots: 1, MaxConcurrency: 1, CredentialFlags: map[string]bool{"has_env:GITHUB_TOKEN": false}})
	_ = bus.Publish(ctx, subjects.Heartbeat, hbEnv)

	snapshot := TaskGraphSnapshotPayload{
		SchemaVersion: InboxSchemaVersionV1,
		Graphs: []TaskGraphSnapshot{{
			GraphRef: "g",
			Nodes: []TaskGraphNode{{
				TaskID:   "task-cred",
				GraphRef: "g",
				Status:   contracts.TaskStatusOpen,
				TaskRef:  TaskRef{BackendType: "github", BackendNativeID: "task-cred"},
				Requirements: []TaskRequirement{
					{Name: "implement", Kind: "capability"},
					{Name: "has_env:GITHUB_TOKEN", Kind: "credential_flag"},
				},
			}},
		}},
	}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)

	deadline := time.After(2 * time.Second)
	for {
		status, data := backend.status("task-cred")
		if status == contracts.TaskStatusBlocked {
			if !strings.Contains(strings.ToLower(data[inboxStatusCommentKey]), "no executor") {
				t.Fatalf("expected blocked reason to mention no executor, got %#v", data)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for blocked status update")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestMastermindSchedulerBlocksTaskWhenNoExecutorsRegistered(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-blocked-empty-fleet")
	backend := &fakeTaskStatusBackend{t: t}
	mastermind := NewMastermind(MastermindOptions{
		ID:             "m",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	snapshot := TaskGraphSnapshotPayload{
		SchemaVersion: InboxSchemaVersionV1,
		Graphs: []TaskGraphSnapshot{{
			GraphRef: "g",
			Nodes: []TaskGraphNode{{
				TaskID:   "task-no-executors",
				GraphRef: "g",
				Status:   contracts.TaskStatusOpen,
				TaskRef:  TaskRef{BackendType: "tk", BackendNativeID: "task-no-executors"},
				Requirements: []TaskRequirement{
					{Name: "implement", Kind: "capability"},
				},
			}},
		}},
	}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)

	deadline := time.After(2 * time.Second)
	for {
		status, data := backend.status("task-no-executors")
		if status == contracts.TaskStatusBlocked {
			if !strings.Contains(strings.ToLower(data[inboxStatusCommentKey]), "no executor") {
				t.Fatalf("expected blocked reason to mention no executor, got %#v", data)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for blocked status update")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestMastermindSchedulerBlocksTaskWhenExecutorMissingCapabilities(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-blocked-empty-capabilities")
	backend := &fakeTaskStatusBackend{t: t}
	mastermind := NewMastermind(MastermindOptions{
		ID:             "m",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	hbEnv, _ := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "", ExecutorHeartbeatPayload{
		ExecutorID:     "exec-1",
		AvailableSlots: 1,
		MaxConcurrency: 1,
	})
	_ = bus.Publish(ctx, subjects.Heartbeat, hbEnv)

	snapshot := TaskGraphSnapshotPayload{
		SchemaVersion: InboxSchemaVersionV1,
		Graphs: []TaskGraphSnapshot{{
			GraphRef: "g",
			Nodes: []TaskGraphNode{{
				TaskID:   "task-missing-capabilities",
				GraphRef: "g",
				Status:   contracts.TaskStatusOpen,
				TaskRef:  TaskRef{BackendType: "tk", BackendNativeID: "task-missing-capabilities"},
				Requirements: []TaskRequirement{
					{Name: "implement", Kind: "capability"},
				},
			}},
		}},
	}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)

	deadline := time.After(2 * time.Second)
	for {
		status, data := backend.status("task-missing-capabilities")
		if status == contracts.TaskStatusBlocked {
			if !strings.Contains(strings.ToLower(data[inboxStatusCommentKey]), "no executor") {
				t.Fatalf("expected blocked reason to mention no executor, got %#v", data)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for blocked status update")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestMastermindSchedulerDefaultGitWorkspaceForGitHubTasks(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-git-default")
	mastermind := NewMastermind(MastermindOptions{ID: "m", Bus: bus, Subjects: subjects, RegistryTTL: 2 * time.Second, RequestTimeout: 2 * time.Second})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	registerExecutorWithCapabilities(t, ctx, bus, subjects, "exec-1", 1, CapabilityImplement)
	hbEnv, _ := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "", ExecutorHeartbeatPayload{ExecutorID: "exec-1", AvailableSlots: 1, MaxConcurrency: 1})
	_ = bus.Publish(ctx, subjects.Heartbeat, hbEnv)

	snapshot := TaskGraphSnapshotPayload{SchemaVersion: InboxSchemaVersionV1, Graphs: []TaskGraphSnapshot{{
		GraphRef: "g",
		Nodes: []TaskGraphNode{{
			TaskID:   "gh-1",
			GraphRef: "g",
			Status:   contracts.TaskStatusOpen,
			TaskRef:  TaskRef{BackendType: "github", BackendNativeID: "gh-1"},
			SourceContext: SourceContext{
				Provider:   "github",
				Repository: "org/repo",
			},
			Requirements: []TaskRequirement{{Name: "implement", Kind: "capability"}},
		}},
	}}}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)

	msgs, stop, err := bus.ConsumeQueue(ctx, "queue.tasks.implement", QueueConsumeOptions{Consumer: "probe"})
	if err != nil {
		t.Fatalf("consume queue: %v", err)
	}
	defer stop()
	select {
	case msg := <-msgs:
		payload := QueueTaskMessage{}
		if err := json.Unmarshal(msg.Event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal queue payload: %v", err)
		}
		if payload.WorkspaceSpec.Kind != QueueWorkspaceGit {
			t.Fatalf("expected default workspace kind git, got %#v", payload.WorkspaceSpec)
		}
		if payload.WorkspaceSpec.Git == nil || payload.WorkspaceSpec.Git.RepoURL != "https://github.com/org/repo" {
			t.Fatalf("expected default github repo URL, got %#v", payload.WorkspaceSpec.Git)
		}
		_ = msg.Ack(ctx)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for queued task")
	}
}

func TestMastermindSchedulerEnqueueIsIdempotentForRepeatedSnapshots(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("sched-idem")
	mastermind := NewMastermind(MastermindOptions{ID: "m", Bus: bus, Subjects: subjects, RegistryTTL: 2 * time.Second, RequestTimeout: 2 * time.Second})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	registerExecutorWithCapabilities(t, ctx, bus, subjects, "exec-1", 2, CapabilityImplement)
	hbEnv, _ := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "", ExecutorHeartbeatPayload{ExecutorID: "exec-1", AvailableSlots: 2, MaxConcurrency: 2})
	_ = bus.Publish(ctx, subjects.Heartbeat, hbEnv)

	snapshot := TaskGraphSnapshotPayload{SchemaVersion: InboxSchemaVersionV1, Graphs: []TaskGraphSnapshot{{
		GraphRef: "g",
		Nodes: []TaskGraphNode{{
			TaskID:        "dup-1",
			GraphRef:      "g",
			Status:        contracts.TaskStatusOpen,
			TaskRef:       TaskRef{BackendType: "tk", BackendNativeID: "dup-1"},
			Requirements:  []TaskRequirement{{Name: "implement", Kind: "capability"}},
			WorkspaceSpec: &WorkspaceSpec{Kind: "none"},
		}},
	}}}
	env, _ := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", snapshot)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)
	_ = bus.Publish(ctx, subjects.TaskGraphSnapshot, env)

	msgs, stop, err := bus.ConsumeQueue(ctx, "queue.tasks.implement", QueueConsumeOptions{Consumer: "probe"})
	if err != nil {
		t.Fatalf("consume queue: %v", err)
	}
	defer stop()
	count := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case msg := <-msgs:
			count++
			_ = msg.Ack(ctx)
		case <-timeout:
			break loop
		}
	}
	if count != 1 {
		t.Fatalf("expected idempotent enqueue count=1, got %d", count)
	}
}
