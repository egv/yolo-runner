package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestDistributedE2ESmokeHarness(t *testing.T) {
	runDistributedSmokeHarness(t)
}

type distributedSmokeRunner struct {
	transport string
}

func (r distributedSmokeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:      "runner_output",
			Message:   "distributed smoke run",
			Timestamp: time.Now().UTC(),
		})
	}
	return contracts.RunnerResult{
		Status:    contracts.RunnerResultCompleted,
		Artifacts: map[string]string{"transport": r.transport},
	}, nil
}

func runDistributedSmokeHarness(t *testing.T) {
	t.Helper()

	testCases := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "redis", run: runDistributedSmokeRedis},
		{name: "nats", run: runDistributedSmokeNATS},
	}

	for _, tc := range testCases {
		t.Run(tc.name, tc.run)
	}
}

func runDistributedSmokeRedis(t *testing.T) {
	t.Helper()
	address := strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_SMOKE_REDIS_ADDR"))
	if address == "" {
		server := miniredis.RunT(t)
		address = "redis://" + server.Addr()
	}

	bus, err := NewRedisBus(address, BusBackendOptions{Stream: "smoke-tasks", Group: "smoke-workers", Durable: "smoke-durable"})
	if err != nil {
		t.Fatalf("create redis bus: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Close()
	})

	runDistributedSmokeScenario(t, "redis", bus)
}

func runDistributedSmokeNATS(t *testing.T) {
	t.Helper()
	address := strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_SMOKE_NATS_ADDR"))
	shutdown := func() {}
	if address == "" {
		var stop func()
		address, stop = startNATSServer(t)
		shutdown = stop
	}
	t.Cleanup(shutdown)

	bus, err := NewNATSBus(address, BusBackendOptions{Stream: "SMOKE_TASKS", Group: "smoke-workers", Durable: "smoke-durable"})
	if err != nil {
		t.Fatalf("create nats bus: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Close()
	})

	runDistributedSmokeScenario(t, "nats", bus)
}

func runDistributedSmokeScenario(t *testing.T, backend string, bus Bus) {
	t.Helper()

	writer := newDistributedSmokeLogWriter(t, backend)
	writeLog := func(stage string, fields map[string]string) {
		writer.Log(stage, fields)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	subjects := DefaultEventSubjects("distributed-smoke-" + backend)
	statusBackend := &fakeTaskStatusBackend{t: t}
	statusBackend.SetTaskTree("root-smoke", &contracts.TaskTree{
		Root: contracts.Task{ID: "root-smoke", Title: "Smoke Root", Status: contracts.TaskStatusOpen},
		Tasks: map[string]contracts.Task{
			"root-smoke": {ID: "root-smoke", Title: "Smoke Root", Status: contracts.TaskStatusOpen},
		},
	})

	mastermind := NewMastermind(MastermindOptions{
		ID:                    "mastermind-" + backend,
		Bus:                   bus,
		Subjects:              subjects,
		RegistryTTL:           2 * time.Second,
		RequestTimeout:        2 * time.Second,
		TaskGraphSyncRoots:    []string{"root-smoke"},
		TaskGraphSyncInterval: 50 * time.Millisecond,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": statusBackend,
		},
		StatusUpdateAuthToken: "smoke-token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	registerCh, unregister, err := bus.Subscribe(ctx, subjects.Register)
	if err != nil {
		t.Fatalf("subscribe register: %v", err)
	}
	defer unregister()

	dispatchCh, unsubscribeDispatch, err := bus.Subscribe(ctx, subjects.TaskDispatch)
	if err != nil {
		t.Fatalf("subscribe dispatch: %v", err)
	}
	defer unsubscribeDispatch()

	monitorCh, unsubscribeMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor: %v", err)
	}
	defer unsubscribeMonitor()

	ackCh, unsubscribeAck, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateAck)
	if err != nil {
		t.Fatalf("subscribe task status ack: %v", err)
	}
	defer unsubscribeAck()

	graphCh, unsubscribeGraph, err := mastermind.SubscribeTaskGraph(ctx, TaskGraphSubscriptionFilter{Backends: []string{"tk"}})
	if err != nil {
		t.Fatalf("subscribe task graph: %v", err)
	}
	defer unsubscribeGraph()

	executorID := "executor-" + backend
	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           executorID,
		Bus:          bus,
		Runner:       distributedSmokeRunner{transport: backend},
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() {
		_ = executor.Start(ctx)
	}()

	registration := waitForExecutorRegistration(t, registerCh, executorID)
	writeLog("executor_registered", map[string]string{"executor_id": registration.ExecutorID})
	waitForExecutorAvailable(t, mastermind, CapabilityImplement)

	snapshot := waitForTaskGraphSnapshot(t, graphCh)
	writeLog("task_graph_published", map[string]string{"backend": snapshot.Backend, "root_id": snapshot.RootID})

	taskID := backend + "-smoke-task"
	result, err := mastermind.DispatchTask(ctx, TaskDispatchRequest{
		RunnerRequest: contracts.RunnerRequest{
			TaskID: taskID,
			Mode:   contracts.RunnerModeImplement,
			Prompt: "distributed smoke",
		},
	})
	if err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q", result.Status)
	}
	writeLog("executor_completed", map[string]string{"task_id": taskID, "result": string(result.Status)})

	dispatch := waitForTaskDispatch(t, dispatchCh, taskID)
	if strings.TrimSpace(dispatch.TargetExecutorID) != executorID {
		t.Fatalf("expected task dispatch target executor %q, got %q", executorID, dispatch.TargetExecutorID)
	}
	writeLog("mastermind_assigned", map[string]string{"task_id": dispatch.TaskID, "target_executor_id": dispatch.TargetExecutorID})

	waitForRunnerLifecycleFinished(t, monitorCh, taskID)
	writeLog("runner_finished", map[string]string{"task_id": taskID})

	comment := "distributed smoke status update"
	commandID, err := mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		Backends:  []string{"tk"},
		TaskID:    taskID,
		Status:    contracts.TaskStatusClosed,
		Comment:   comment,
		AuthToken: "smoke-token",
		Metadata:  map[string]string{"source": "distributed-smoke"},
	})
	if err != nil {
		t.Fatalf("publish task status update: %v", err)
	}
	ack := waitForTaskStatusAck(t, ackCh, commandID)
	if !ack.Success {
		t.Fatalf("expected status update ack success=true, got %+v", ack)
	}
	taskStatus, taskData := statusBackend.status(taskID)
	if taskStatus != contracts.TaskStatusClosed {
		t.Fatalf("expected task status %q, got %q", contracts.TaskStatusClosed, taskStatus)
	}
	if taskData[inboxStatusCommentKey] != comment {
		t.Fatalf("expected comment %q, got %q", comment, taskData[inboxStatusCommentKey])
	}
	writeLog("status_update_applied", map[string]string{"task_id": taskID, "command_id": commandID})
}

func waitForExecutorAvailable(t *testing.T, mastermind *Mastermind, capability Capability) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mastermind != nil {
			if _, err := mastermind.Registry().Pick(capability); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for executor capability %q", capability)
}

func waitForExecutorRegistration(t *testing.T, ch <-chan EventEnvelope, executorID string) ExecutorRegistrationPayload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("executor registration channel closed")
			}
			if raw.Type != EventTypeExecutorRegistered {
				continue
			}
			payload := ExecutorRegistrationPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.ExecutorID) != executorID {
				continue
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for executor registration %q", executorID)
		}
	}
}

func waitForTaskDispatch(t *testing.T, ch <-chan EventEnvelope, taskID string) TaskDispatchPayload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("task dispatch channel closed")
			}
			if raw.Type != EventTypeTaskDispatch {
				continue
			}
			payload := TaskDispatchPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.TaskID) != taskID {
				continue
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for task dispatch %q", taskID)
		}
	}
}

func waitForTaskGraphSnapshot(t *testing.T, ch <-chan TaskGraphEvent) TaskGraphSnapshotPayload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatalf("task graph channel closed")
			}
			if event.Type != EventTypeTaskGraphSnapshot || event.Snapshot == nil {
				continue
			}
			if strings.TrimSpace(event.Snapshot.RootID) == "" {
				continue
			}
			return *event.Snapshot
		case <-timeout:
			t.Fatalf("timed out waiting for task graph snapshot")
		}
	}
}

func waitForRunnerLifecycleFinished(t *testing.T, ch <-chan EventEnvelope, taskID string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	sawStarted := false
	sawFinished := false
	for !sawStarted || !sawFinished {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("monitor channel closed")
			}
			if raw.Type != EventTypeMonitorEvent {
				continue
			}
			payload := MonitorEventPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Event.TaskID) != taskID {
				continue
			}
			switch payload.Event.Type {
			case contracts.EventTypeRunnerStarted:
				sawStarted = true
			case contracts.EventTypeRunnerFinished:
				sawFinished = true
			}
		case <-timeout:
			t.Fatalf("timed out waiting for runner lifecycle events task=%s started=%t finished=%t", taskID, sawStarted, sawFinished)
		}
	}
}

func waitForTaskStatusAck(t *testing.T, ch <-chan EventEnvelope, commandID string) TaskStatusUpdateAckPayload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("status ack channel closed")
			}
			if raw.Type != EventTypeTaskStatusAck {
				continue
			}
			payload := TaskStatusUpdateAckPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.CommandID) != commandID {
				continue
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for status ack command %q", commandID)
		}
	}
}

type distributedSmokeLogWriter struct {
	path string
	enc  *json.Encoder
	file *os.File
}

func newDistributedSmokeLogWriter(t *testing.T, backend string) *distributedSmokeLogWriter {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_SMOKE_EVENTS_DIR"))
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create smoke events dir: %v", err)
	}
	name := fmt.Sprintf("distributed-smoke-%s-%s.events.jsonl", backend, time.Now().UTC().Format("20060102_150405"))
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create smoke event log: %v", err)
	}
	writer := &distributedSmokeLogWriter{path: path, enc: json.NewEncoder(file), file: file}
	t.Cleanup(func() {
		_ = writer.file.Close()
	})
	t.Logf("distributed smoke events log (%s): %s", backend, path)
	return writer
}

func (w *distributedSmokeLogWriter) Log(stage string, fields map[string]string) {
	if w == nil || w.enc == nil {
		return
	}
	entry := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"stage":     strings.TrimSpace(stage),
	}
	if len(fields) > 0 {
		entry["fields"] = fields
	}
	_ = w.enc.Encode(entry)
}
