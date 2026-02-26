package distributed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type runtimeTestRunner struct {
	mu       sync.Mutex
	attempts int
	runFn    func(context.Context, contracts.RunnerRequest, int) (contracts.RunnerResult, error)
	lastReq  contracts.RunnerRequest
}

func (r *runtimeTestRunner) Run(ctx context.Context, req contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	r.attempts++
	attempt := r.attempts
	r.lastReq = req
	r.mu.Unlock()
	if r.runFn == nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	return r.runFn(ctx, req, attempt)
}

func (r *runtimeTestRunner) Attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

func TestQueueWorkerRuntimeConsumeRunEmitLifecycle(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("queue-runtime")
	runner := &runtimeTestRunner{}
	runner.runFn = func(_ context.Context, req contracts.RunnerRequest, _ int) (contracts.RunnerResult, error) {
		if req.OnProgress != nil {
			req.OnProgress(contracts.RunnerProgress{Type: "runner_output", Message: "hello", Timestamp: time.Now().UTC()})
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	worker := NewQueueWorkerRuntime(QueueWorkerRuntimeOptions{
		ID:                      "worker-1",
		InstanceID:              "worker-1-instance",
		Hostname:                "host-a",
		Bus:                     bus,
		Runner:                  runner,
		Queue:                   "queue.tasks",
		QueueGroup:              "workers",
		Subjects:                subjects,
		Capabilities:            []Capability{CapabilityImplement},
		MaxConcurrency:          2,
		CapabilityProbeInterval: 25 * time.Millisecond,
		HeartbeatInterval:       25 * time.Millisecond,
		CapabilityProbe: func(context.Context) QueueWorkerCapabilitySnapshot {
			return QueueWorkerCapabilitySnapshot{
				EnvironmentProbes: ExecutorEnvironmentFeatureProbes{HasGit: true, HasGo: true, OS: "linux", Arch: "amd64"},
				CredentialFlags:   map[string]bool{"has_env:GITHUB_TOKEN": true},
				ResourceHints:     ExecutorResourceHints{CPUCores: 8, MemGB: 16},
			}
		},
	})

	go func() { _ = worker.Start(ctx) }()

	monitorCh, unsubMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor: %v", err)
	}
	defer unsubMonitor()

	heartbeatCh, unsubHeartbeat, err := bus.Subscribe(ctx, subjects.Heartbeat)
	if err != nil {
		t.Fatalf("subscribe heartbeat: %v", err)
	}
	defer unsubHeartbeat()

	msg := QueueTaskMessage{
		TaskRef:       QueueTaskRef{Backend: "tk", NativeID: "154"},
		WorkspaceSpec: QueueWorkspaceSpec{Kind: QueueWorkspaceNone},
		Requirements:  QueueTaskRequirements{Capabilities: []Capability{CapabilityImplement}},
		GraphRef:      "root-130",
		Request:       mustTransportRequest(t, contracts.RunnerRequest{TaskID: "task-154", Metadata: map[string]string{"backend": "default"}}),
	}
	env, err := NewEventEnvelope(EventTypeTaskDispatch, "mastermind", "corr-154", msg)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", env); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	sawStarted := false
	sawCompleted := false
	sawOutput := false
	timeout := time.After(2 * time.Second)
	for !sawStarted || !sawCompleted || !sawOutput {
		select {
		case raw := <-monitorCh:
			if raw.Type != EventTypeMonitorEvent || len(raw.Payload) == 0 {
				continue
			}
			payload := MonitorEventPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal monitor payload: %v", err)
			}
			switch payload.Event.Type {
			case contracts.EventTypeTaskStarted:
				sawStarted = true
			case contracts.EventTypeTaskCompleted:
				sawCompleted = true
			case contracts.EventTypeRunnerOutput:
				sawOutput = true
			}
		case <-timeout:
			t.Fatalf("timed out waiting for lifecycle events; started=%t completed=%t output=%t", sawStarted, sawCompleted, sawOutput)
		}
	}

	select {
	case raw := <-heartbeatCh:
		payload := ExecutorHeartbeatPayload{}
		if err := json.Unmarshal(raw.Payload, &payload); err != nil {
			t.Fatalf("unmarshal heartbeat: %v", err)
		}
		if payload.EnvironmentProbes.OS == "" || payload.ResourceHints.CPUCores == 0 {
			t.Fatalf("expected probe payload in heartbeat, got %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting heartbeat")
	}
}

func TestQueueWorkerRuntimeNacksTransientFailuresForRedelivery(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("queue-redelivery")
	runner := &runtimeTestRunner{}
	runner.runFn = func(_ context.Context, _ contracts.RunnerRequest, attempt int) (contracts.RunnerResult, error) {
		if attempt == 1 {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, MarkTransient(errors.New("temporary transport issue"))
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	worker := NewQueueWorkerRuntime(QueueWorkerRuntimeOptions{
		ID:                "worker-2",
		Bus:               bus,
		Runner:            runner,
		Queue:             "queue.tasks",
		QueueGroup:        "workers",
		Subjects:          subjects,
		Capabilities:      []Capability{CapabilityImplement},
		HeartbeatInterval: 25 * time.Millisecond,
	})
	go func() { _ = worker.Start(ctx) }()

	monitorCh, unsubMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor: %v", err)
	}
	defer unsubMonitor()

	msg := QueueTaskMessage{
		TaskRef:       QueueTaskRef{Backend: "tk", NativeID: "155"},
		WorkspaceSpec: QueueWorkspaceSpec{Kind: QueueWorkspaceNone},
		Requirements:  QueueTaskRequirements{Capabilities: []Capability{CapabilityImplement}},
		GraphRef:      "root-130",
		Request:       mustTransportRequest(t, contracts.RunnerRequest{TaskID: "task-155"}),
	}
	env, err := NewEventEnvelope(EventTypeTaskDispatch, "mastermind", "corr-155", msg)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", env); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	timeout := time.After(2 * time.Second)
	for {
		select {
		case raw := <-monitorCh:
			if raw.Type != EventTypeMonitorEvent || len(raw.Payload) == 0 {
				continue
			}
			payload := MonitorEventPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal monitor payload: %v", err)
			}
			if payload.Event.Type == contracts.EventTypeTaskCompleted {
				if runner.Attempts() < 2 {
					t.Fatalf("expected at least 2 attempts for transient redelivery, got %d", runner.Attempts())
				}
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for completion after redelivery; attempts=%d", runner.Attempts())
		}
	}
}

func TestQueueWorkerRuntimePreparesGitWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for workspace test")
	}

	repo := initGitRepo(t)
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("queue-git")

	runner := &runtimeTestRunner{}
	runner.runFn = func(_ context.Context, req contracts.RunnerRequest, _ int) (contracts.RunnerResult, error) {
		if req.RepoRoot == "" {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, fmt.Errorf("expected repo root")
		}
		cmd := exec.Command("git", "-C", req.RepoRoot, "rev-parse", "--abbrev-ref", "HEAD")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, err
		}
		branch := strings.TrimSpace(string(out))
		if !strings.HasPrefix(branch, "work-task-156") {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, fmt.Errorf("unexpected branch %q", branch)
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	workspaceRoot := t.TempDir()
	worker := NewQueueWorkerRuntime(QueueWorkerRuntimeOptions{
		ID: "worker-3", Bus: bus, Runner: runner, Queue: "queue.tasks", QueueGroup: "workers", Subjects: subjects,
		Capabilities: []Capability{CapabilityImplement}, WorkspaceRoot: workspaceRoot,
	})
	go func() { _ = worker.Start(ctx) }()

	monitorCh, unsubMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor: %v", err)
	}
	defer unsubMonitor()

	msg := QueueTaskMessage{
		TaskRef:       QueueTaskRef{Backend: "tk", NativeID: "156"},
		WorkspaceSpec: QueueWorkspaceSpec{Kind: QueueWorkspaceGit, Git: &QueueGitWorkspaceSpec{RepoURL: repo, BaseRef: "main", WorkBranch: "work-task-156"}},
		Requirements:  QueueTaskRequirements{Capabilities: []Capability{CapabilityImplement}},
		GraphRef:      "root-130",
		Request:       mustTransportRequest(t, contracts.RunnerRequest{TaskID: "task-156"}),
	}
	env, err := NewEventEnvelope(EventTypeTaskDispatch, "mastermind", "corr-156", msg)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", env); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	timeout := time.After(3 * time.Second)
	for {
		select {
		case raw := <-monitorCh:
			if raw.Type != EventTypeMonitorEvent || len(raw.Payload) == 0 {
				continue
			}
			payload := MonitorEventPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal monitor payload: %v", err)
			}
			if payload.Event.Type == contracts.EventTypeTaskCompleted {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for git workspace completion")
		}
	}
}

func TestQueueWorkerRuntimeDefaultProbeIncludesCredentialFlagsInRegistrationAndHeartbeat(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token-for-worker-runtime-test")

	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("queue-default-probe")
	registerCh, unsubRegister, err := bus.Subscribe(ctx, subjects.Register)
	if err != nil {
		t.Fatalf("subscribe register: %v", err)
	}
	defer unsubRegister()

	heartbeatCh, unsubHeartbeat, err := bus.Subscribe(ctx, subjects.Heartbeat)
	if err != nil {
		t.Fatalf("subscribe heartbeat: %v", err)
	}
	defer unsubHeartbeat()

	worker := NewQueueWorkerRuntime(QueueWorkerRuntimeOptions{
		ID:                "worker-default-probe",
		Bus:               bus,
		Runner:            &runtimeTestRunner{},
		Queue:             "queue.tasks",
		QueueGroup:        "workers",
		Subjects:          subjects,
		HeartbeatInterval: 20 * time.Millisecond,
	})
	go func() { _ = worker.Start(ctx) }()

	select {
	case raw := <-registerCh:
		payload := ExecutorRegistrationPayload{}
		if err := json.Unmarshal(raw.Payload, &payload); err != nil {
			t.Fatalf("unmarshal register payload: %v", err)
		}
		if payload.CredentialFlags["has_env:GITHUB_TOKEN"] != true {
			t.Fatalf("expected registration credential flag has_env:GITHUB_TOKEN=true, got %#v", payload.CredentialFlags)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for registration payload")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case raw := <-heartbeatCh:
			payload := ExecutorHeartbeatPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal heartbeat payload: %v", err)
			}
			if payload.CredentialFlags["has_env:GITHUB_TOKEN"] == true {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat with credential flags")
		}
	}
}

func TestQueueWorkerRuntimeHeartbeatContinuesWhenConcurrencySaturated(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("queue-saturated-heartbeat")
	heartbeatCh, unsubHeartbeat, err := bus.Subscribe(ctx, subjects.Heartbeat)
	if err != nil {
		t.Fatalf("subscribe heartbeat: %v", err)
	}
	defer unsubHeartbeat()

	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	worker := NewQueueWorkerRuntime(QueueWorkerRuntimeOptions{
		ID:                "worker-saturated",
		Bus:               bus,
		Runner:            runner,
		Queue:             "queue.tasks",
		QueueGroup:        "workers",
		Subjects:          subjects,
		MaxConcurrency:    1,
		HeartbeatInterval: 20 * time.Millisecond,
	})
	go func() { _ = worker.Start(ctx) }()

	first := QueueTaskMessage{
		TaskRef:       QueueTaskRef{Backend: "tk", NativeID: "first"},
		WorkspaceSpec: QueueWorkspaceSpec{Kind: QueueWorkspaceNone},
		Request:       mustTransportRequest(t, contracts.RunnerRequest{TaskID: "first"}),
	}
	firstEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "mastermind", "corr-first", first)
	if err != nil {
		t.Fatalf("new first envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", firstEnv); err != nil {
		t.Fatalf("enqueue first: %v", err)
	}

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first task did not start")
	}

	second := QueueTaskMessage{
		TaskRef:       QueueTaskRef{Backend: "tk", NativeID: "second"},
		WorkspaceSpec: QueueWorkspaceSpec{Kind: QueueWorkspaceNone},
		Request:       mustTransportRequest(t, contracts.RunnerRequest{TaskID: "second"}),
	}
	secondEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "mastermind", "corr-second", second)
	if err != nil {
		t.Fatalf("new second envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", secondEnv); err != nil {
		t.Fatalf("enqueue second: %v", err)
	}

	waitInflightDeadline := time.After(600 * time.Millisecond)
	for {
		bus.mu.RLock()
		inflight := len(bus.inflight)
		bus.mu.RUnlock()
		if inflight >= 2 {
			break
		}
		select {
		case <-waitInflightDeadline:
			close(runner.release)
			t.Fatalf("timed out waiting for second queue message to enter inflight; inflight=%d", inflight)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	marker := time.Now().UTC()
	deadline := time.After(600 * time.Millisecond)
	for {
		select {
		case raw := <-heartbeatCh:
			payload := ExecutorHeartbeatPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal heartbeat payload: %v", err)
			}
			if payload.SeenAt.After(marker) && payload.CurrentLoad >= 1 {
				close(runner.release)
				return
			}
		case <-deadline:
			close(runner.release)
			t.Fatal("timed out waiting for post-enqueue heartbeat while worker was saturated")
		}
	}
}

func mustTransportRequest(t *testing.T, req contracts.RunnerRequest) json.RawMessage {
	t.Helper()
	raw, err := requestForTransport(req)
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	return raw
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir, "config", "user.email", "runtime@example.com")
	runGit(t, repoDir, "config", "user.name", "runtime-test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("runtime\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runGit(t, repoDir, "add", "README.md")
	runGit(t, repoDir, "commit", "-m", "init")
	return repoDir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}
