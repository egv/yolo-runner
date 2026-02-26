package distributed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type fakeRunner struct {
	result contracts.RunnerResult
	err    error
}

func (r fakeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	return r.result, r.err
}

type scriptedRunner struct {
	mu    sync.Mutex
	calls int
	runFn func(attempt int, ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error)
}

func (r *scriptedRunner) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	r.calls++
	attempt := r.calls
	r.mu.Unlock()
	if r.runFn == nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	return r.runFn(attempt, ctx, request)
}

func (r *scriptedRunner) attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type scriptedReviewRunner struct {
	mu    sync.Mutex
	calls int
	runFn func(attempt int, ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error)
}

func (r *scriptedReviewRunner) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	r.calls++
	attempt := r.calls
	r.mu.Unlock()
	if r.runFn == nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	return r.runFn(attempt, ctx, request)
}

func (r *scriptedReviewRunner) attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestParseEventEnvelopeSupportsLegacyAndV1Schemas(t *testing.T) {
	t.Run("legacy event defaults to v0", func(t *testing.T) {
		legacyPayload := []byte(`{"type":"executor_registered","source":"old-exec","payload":{"executor_id":"exec-1","capabilities":["implement"]}}`)
		evt, err := ParseEventEnvelope(legacyPayload)
		if err != nil {
			t.Fatalf("parse legacy envelope: %v", err)
		}
		if evt.SchemaVersion != EventSchemaVersionV0 {
			t.Fatalf("expected legacy schema version %q, got %q", EventSchemaVersionV0, evt.SchemaVersion)
		}
	})

	t.Run("versioned event preserves schema and type", func(t *testing.T) {
		msg, err := NewEventEnvelope(EventTypeExecutorHeartbeat, "exec", "corr", ExecutorHeartbeatPayload{ExecutorID: "exec"})
		if err != nil {
			t.Fatalf("new envelope: %v", err)
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		parsed, err := ParseEventEnvelope(raw)
		if err != nil {
			t.Fatalf("parse envelope: %v", err)
		}
		if parsed.SchemaVersion != EventSchemaVersionV1 {
			t.Fatalf("expected schema %q, got %q", EventSchemaVersionV1, parsed.SchemaVersion)
		}
		if parsed.Type != EventTypeExecutorHeartbeat {
			t.Fatalf("expected type %q, got %q", EventTypeExecutorHeartbeat, parsed.Type)
		}
	})
}

func TestExecutorRegistryRoutesByCapabilitiesAndEvictsStale(t *testing.T) {
	registry := NewExecutorRegistry(20*time.Millisecond, func() time.Time { return time.Now().UTC() })
	registry.Register(ExecutorRegistrationPayload{ExecutorID: "implement-only", Capabilities: []Capability{CapabilityImplement}})
	registry.Register(ExecutorRegistrationPayload{ExecutorID: "reviewer", Capabilities: []Capability{CapabilityReview, CapabilityImplement}})

	reviewer, err := registry.Pick(CapabilityReview)
	if err != nil {
		t.Fatalf("expected review executor, got error %v", err)
	}
	if reviewer.ID != "reviewer" {
		t.Fatalf("expected reviewer, got %q", reviewer.ID)
	}

	// advance clock forward to expire entries
	time.Sleep(30 * time.Millisecond)
	_, err = registry.Pick(CapabilityReview)
	if err == nil {
		t.Fatalf("expected stale registry to return no capable executors")
	}
}

func TestExecutorRegistryUsesReceiptTimeForLiveness(t *testing.T) {
	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	registry := NewExecutorRegistry(2*time.Second, func() time.Time { return clock })

	registry.Register(ExecutorRegistrationPayload{
		ExecutorID:   "skewed-exec",
		Capabilities: []Capability{CapabilityImplement},
		StartedAt:    now.Add(-10 * time.Minute), // stale remote timestamp should not mark executor offline
	})
	if _, err := registry.Pick(CapabilityImplement); err != nil {
		t.Fatalf("expected executor to be available after registration despite skewed timestamp: %v", err)
	}

	registry.Heartbeat(ExecutorHeartbeatPayload{
		ExecutorID: "skewed-exec",
		SeenAt:     now.Add(-9 * time.Minute), // stale remote timestamp should not evict executor
	})
	if _, err := registry.Pick(CapabilityImplement); err != nil {
		t.Fatalf("expected executor to remain available after heartbeat despite skewed timestamp: %v", err)
	}

	clock = clock.Add(3 * time.Second)
	if _, err := registry.Pick(CapabilityImplement); err == nil {
		t.Fatalf("expected executor to expire after ttl based on local receipt time")
	}
}

func TestMastermindRoutesTaskBasedOnCapabilities(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		RequestTimeout: 2 * time.Second,
		RegistryTTL:    2 * time.Second,
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	resultCh := make(chan string, 8)
	reviewExecutor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "review-exec",
		Bus:          bus,
		Runner:       fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"worker": "review"}}},
		Capabilities: []Capability{CapabilityReview},
	})
	implementExecutor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "impl-exec",
		Bus:          bus,
		Runner:       fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"worker": "implement"}}, err: fmt.Errorf("should not execute")},
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() { _ = reviewExecutor.Start(ctx) }()
	go func() { _ = implementExecutor.Start(ctx) }()
	_ = resultCh

	time.Sleep(20 * time.Millisecond)
	result, err := mastermind.DispatchTask(ctx, TaskDispatchRequest{
		RunnerRequest: contracts.RunnerRequest{TaskID: "task-review", Mode: contracts.RunnerModeReview},
	})
	if err != nil {
		t.Fatalf("dispatch review task: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q", result.Status)
	}
	if result.Artifacts["worker"] != "review" {
		t.Fatalf("expected review worker result, got %v", result.Artifacts)
	}
}

func TestExecutorWorkerRetriesRequestUntilSuccess(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	runner := &scriptedRunner{}
	runner.runFn = func(attempt int, _ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
		if attempt == 1 {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, fmt.Errorf("temporary failure")
		}
		request.Metadata["attempt"] = "2"
		return contracts.RunnerResult{
			Status:    contracts.RunnerResultCompleted,
			Reason:    "recovered",
			StartedAt: time.Now().UTC(),
			Artifacts: map[string]string{"attempt": request.Metadata["attempt"]},
		}, nil
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "retry-exec",
		Bus:          bus,
		Runner:       runner,
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
		MaxRetries:   1,
	})
	go func() { _ = executor.Start(ctx) }()

	time.Sleep(20 * time.Millisecond)

	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	request := contracts.RunnerRequest{
		TaskID:     "retry-task",
		Metadata:   map[string]string{},
		MaxRetries: 1,
	}
	requestRaw, err := requestForTransport(request)
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "retry-correlation",
		TaskID:               "retry-task",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "retry-correlation", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	resultPayload := readTaskResultPayload(t, resultCh)
	if resultPayload.CorrelationID != "retry-correlation" {
		t.Fatalf("expected correlation %q, got %q", "retry-correlation", resultPayload.CorrelationID)
	}
	if resultPayload.Result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q", resultPayload.Result.Status)
	}
	if runner.attempts() != 2 {
		t.Fatalf("expected two attempts with retry, got %d", runner.attempts())
	}
}

func TestExecutorWorkerForwardsRunnerProgressToMonitorEvents(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	runner := &scriptedRunner{}
	runner.runFn = func(_ int, _ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
		if request.OnProgress != nil {
			request.OnProgress(contracts.RunnerProgress{
				Type:      "runner_output",
				Message:   "build step",
				Metadata:  map[string]string{"source": "stdout"},
				Timestamp: time.Now().UTC(),
			})
			request.OnProgress(contracts.RunnerProgress{
				Type:      "runner_output",
				Message:   "warn step",
				Metadata:  map[string]string{"source": "stderr"},
				Timestamp: time.Now().UTC(),
			})
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "progress-exec",
		Bus:          bus,
		Runner:       runner,
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() { _ = executor.Start(ctx) }()

	time.Sleep(20 * time.Millisecond)

	monitorCh, unsubscribeMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor: %v", err)
	}
	defer unsubscribeMonitor()
	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	requestRaw, err := requestForTransport(contracts.RunnerRequest{
		TaskID: "progress-task",
	})
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "progress-correlation",
		TaskID:               "progress-task",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "progress-correlation", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	outputsBySource := map[string]int{}
	gotRunnerStarted := false
	gotRunnerFinished := false
	timeout := time.After(1 * time.Second)
	for !gotRunnerFinished || len(outputsBySource) < 2 {
		select {
		case raw := <-monitorCh:
			if raw.Type != EventTypeMonitorEvent {
				continue
			}
			payload := MonitorEventPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal monitor payload: %v", err)
			}
			switch payload.Event.Type {
			case contracts.EventTypeRunnerStarted:
				gotRunnerStarted = true
			case contracts.EventTypeRunnerFinished:
				gotRunnerFinished = true
			case contracts.EventTypeRunnerOutput:
				source := strings.TrimSpace(payload.Event.Metadata["source"])
				if source == "" {
					source = "stdout"
				}
				outputsBySource[source]++
			}
		case <-timeout:
			t.Fatalf("timed out waiting for progress events")
		}
	}

	_ = readTaskResultPayload(t, resultCh)
	if !gotRunnerStarted || !gotRunnerFinished {
		t.Fatalf("expected runner started and finished events for execution")
	}
	if outputsBySource["stdout"] == 0 {
		t.Fatalf("expected at least one runner_output event with source stdout")
	}
	if outputsBySource["stderr"] == 0 {
		t.Fatalf("expected at least one runner_output event with source stderr")
	}
}

func TestExecutorWorkerCallsExecutionHooksPerAttempt(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	runner := &scriptedRunner{}
	runner.runFn = func(attempt int, _ context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
		if attempt == 1 {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, fmt.Errorf("attempt 1 failed")
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	var preAttempts []int
	var postAttempts []int
	var postErrors []string
	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "hooks-exec",
		Bus:          bus,
		Runner:       runner,
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
		MaxRetries:   1,
		PreExecutionHook: func(_ context.Context, _ contracts.RunnerRequest, attempt int) {
			preAttempts = append(preAttempts, attempt)
		},
		PostExecutionHook: func(_ context.Context, _ contracts.RunnerRequest, result contracts.RunnerResult, err error, attempt int) {
			postAttempts = append(postAttempts, attempt)
			if err != nil {
				postErrors = append(postErrors, err.Error())
				return
			}
			postErrors = append(postErrors, string(result.Status))
		},
	})
	go func() { _ = executor.Start(ctx) }()

	time.Sleep(20 * time.Millisecond)

	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	requestRaw, err := requestForTransport(contracts.RunnerRequest{
		TaskID: "hooks-task",
	})
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "hooks-correlation",
		TaskID:               "hooks-task",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "hooks-correlation", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	result := readTaskResultPayload(t, resultCh)
	if len(preAttempts) != 2 {
		t.Fatalf("expected two pre-execution hook calls, got %d", len(preAttempts))
	}
	if len(postAttempts) != 2 {
		t.Fatalf("expected two post-execution hook calls, got %d", len(postAttempts))
	}
	if preAttempts[0] != 1 || preAttempts[1] != 2 {
		t.Fatalf("expected pre hook attempts [1 2], got %#v", preAttempts)
	}
	if postAttempts[0] != 1 || postAttempts[1] != 2 {
		t.Fatalf("expected post hook attempts [1 2], got %#v", postAttempts)
	}
	if len(postErrors) != 2 {
		t.Fatalf("expected two post hook statuses/errors, got %d", len(postErrors))
	}
	if postErrors[0] != "attempt 1 failed" {
		t.Fatalf("expected first post hook to see first attempt error, got %q", postErrors[0])
	}
	if postErrors[1] != string(result.Result.Status) {
		t.Fatalf("expected second post hook status to match final result, got %q vs %q", postErrors[1], result.Result.Status)
	}
}

func TestExecutorWorkerPreservesCleanStateBetweenRetries(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	var mutatedOnSecondAttempt bool
	runner := &scriptedRunner{}
	runner.runFn = func(_ int, _ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
		if request.Metadata["retry_marker"] != "" {
			mutatedOnSecondAttempt = true
		}
		request.Metadata["retry_marker"] = "dirty"
		if request.Metadata["retry_marker"] == "dirty" && runner.attempts() < 2 {
			return contracts.RunnerResult{}, fmt.Errorf("first attempt fail")
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "state-exec",
		Bus:          bus,
		Runner:       runner,
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
		MaxRetries:   1,
	})
	go func() { _ = executor.Start(ctx) }()

	time.Sleep(20 * time.Millisecond)

	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	requestRaw, err := requestForTransport(contracts.RunnerRequest{
		TaskID:     "state-task",
		Metadata:   map[string]string{"seed": "fresh"},
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "state-correlation",
		TaskID:               "state-task",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "state-correlation", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	_ = readTaskResultPayload(t, resultCh)
	if runner.attempts() != 2 {
		t.Fatalf("expected two attempts with retry, got %d", runner.attempts())
	}
	if mutatedOnSecondAttempt {
		t.Fatalf("expected clean request metadata per attempt")
	}
}

func TestExecutorWorkerSupportsPerAttemptTimeoutAndRetries(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	var secondAttemptSeenTimedOut bool
	runner := &scriptedRunner{}
	runner.runFn = func(attempt int, ctx context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
		if attempt == 1 {
			<-ctx.Done()
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, ctx.Err()
		}
		if ctx.Err() != nil {
			secondAttemptSeenTimedOut = true
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:             "timeout-exec",
		Bus:            bus,
		Runner:         runner,
		Subjects:       subjects,
		Capabilities:   []Capability{CapabilityImplement},
		MaxRetries:     1,
		RequestTimeout: 20 * time.Millisecond,
	})
	go func() { _ = executor.Start(ctx) }()

	time.Sleep(20 * time.Millisecond)

	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	requestRaw, err := requestForTransport(contracts.RunnerRequest{
		TaskID:     "timeout-task",
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "timeout-correlation",
		TaskID:               "timeout-task",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "timeout-correlation", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	result := readTaskResultPayload(t, resultCh)
	if result.Result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result after retry, got %q", result.Result.Status)
	}
	if secondAttemptSeenTimedOut {
		t.Fatalf("expected fresh timeout context per attempt")
	}
}

func readTaskResultPayload(t *testing.T, ch <-chan EventEnvelope) TaskResultPayload {
	t.Helper()
	timeout := time.After(1 * time.Second)
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("task result channel closed")
			}
			if raw.Type != EventTypeTaskResult {
				continue
			}
			payload := TaskResultPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal task result: %v", err)
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for task result")
		}
	}
}

func TestExecutorWorkerSelectsBackendByMetadata(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	var selectedBackend string
	codexRunner := &scriptedRunner{}
	codexRunner.runFn = func(_ int, _ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
		selectedBackend = "codex"
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	opencodeRunner := &scriptedRunner{}
	opencodeRunner.runFn = func(_ int, _ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
		selectedBackend = "opencode"
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:     "backend-exec",
		Bus:    bus,
		Runner: opencodeRunner,
		Backends: map[string]contracts.AgentRunner{
			"codex":    codexRunner,
			"opencode": opencodeRunner,
		},
		Backend:      "opencode",
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() { _ = executor.Start(ctx) }()
	time.Sleep(20 * time.Millisecond)

	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	requestRaw, err := requestForTransport(contracts.RunnerRequest{
		TaskID:   "backend-task",
		Metadata: map[string]string{"backend": "codex"},
	})
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "backend-correlation",
		TaskID:               "backend-task",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "backend-correlation", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	_ = readTaskResultPayload(t, resultCh)
	if selectedBackend != "codex" {
		t.Fatalf("expected backend codex to be selected, got %q", selectedBackend)
	}
	if codexRunner.attempts() != 1 {
		t.Fatalf("expected codex runner to run once, got %d", codexRunner.attempts())
	}
	if opencodeRunner.attempts() != 0 {
		t.Fatalf("expected default runner not used when backend explicitly requested")
	}
}

func TestExecutorCanRequestServiceFromMastermind(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serviceHandled := make(chan string, 1)
	mastermind := NewMastermind(MastermindOptions{
		ID:          "mastermind",
		Bus:         bus,
		RegistryTTL: 2 * time.Second,
		ServiceHandler: func(ctx context.Context, request ServiceRequestPayload) (ServiceResponsePayload, error) {
			serviceHandled <- request.Service
			return ServiceResponsePayload{Artifacts: map[string]string{"service": request.Service}}, nil
		},
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "executor",
		Bus:          bus,
		Runner:       fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() { _ = executor.Start(ctx) }()

	time.Sleep(20 * time.Millisecond)
	response, err := executor.RequestService(ctx, ServiceRequestPayload{TaskID: "t1", Service: "review-with-larger-model"})
	if err != nil {
		t.Fatalf("request service: %v", err)
	}
	if response.Artifacts["service"] != "review-with-larger-model" {
		t.Fatalf("expected service response artifact, got %v", response.Artifacts)
	}
	select {
	case name := <-serviceHandled:
		if name != "review-with-larger-model" {
			t.Fatalf("expected service review-with-larger-model, got %q", name)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("expected service handler to run")
	}
}

func TestExecutorCanRequestReviewAndTaskRewriteServices(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	requestedServices := make(chan string, 2)
	mastermind := NewMastermind(MastermindOptions{
		ID:          "mastermind",
		Bus:         bus,
		RegistryTTL: 2 * time.Second,
		ServiceHandler: func(_ context.Context, request ServiceRequestPayload) (ServiceResponsePayload, error) {
			requestedServices <- request.Service
			return ServiceResponsePayload{
				Artifacts: map[string]string{
					"service": request.Service,
					"task_id": request.TaskID,
				},
			}, nil
		},
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "executor",
		Bus:          bus,
		Runner:       fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() { _ = executor.Start(ctx) }()
	time.Sleep(20 * time.Millisecond)

	reviewResponse, err := executor.RequestReview(ctx, ServiceRequestPayload{TaskID: "t-review"})
	if err != nil {
		t.Fatalf("request review service: %v", err)
	}
	if reviewResponse.Artifacts["service"] != ServiceNameReview {
		t.Fatalf("expected review service response, got %v", reviewResponse.Artifacts)
	}

	rewriteResponse, err := executor.RequestTaskRewrite(ctx, ServiceRequestPayload{TaskID: "t-rewrite"})
	if err != nil {
		t.Fatalf("request task rewrite service: %v", err)
	}
	if rewriteResponse.Artifacts["service"] != ServiceNameTaskRewrite {
		t.Fatalf("expected rewrite service response, got %v", rewriteResponse.Artifacts)
	}

	var received []string
	for i := 0; i < 2; i++ {
		select {
		case service := <-requestedServices:
			received = append(received, service)
		case <-time.After(1 * time.Second):
			t.Fatalf("expected mastermind to receive service request %d", i+1)
		}
	}
	sort.Strings(received)
	if strings.Join(received, ",") != ServiceNameReview+","+ServiceNameTaskRewrite {
		t.Fatalf("expected review and rewrite requests, got %v", received)
	}
}

func TestMastermindReviewRequestHandlerRespondsWithStructuredReviewResult(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reviewRunner := &scriptedReviewRunner{
		runFn: func(_ int, _ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
			if request.Model != "gpt-5.3-codex-large" {
				return contracts.RunnerResult{
					Status: contracts.RunnerResultFailed,
					Reason: "wrong model selected",
				}, fmt.Errorf("wrong model selected")
			}
			return contracts.RunnerResult{
				Status: contracts.RunnerResultCompleted,
				Artifacts: map[string]string{
					"review_verdict":       "fail",
					"review_fail_feedback": "missing tests for retry path",
				},
			}, nil
		},
	}

	var decisions []ReviewDecisionLog
	var decisionsMu sync.Mutex
	handler := NewMastermindReviewRequestHandler(MastermindReviewRequestHandlerOptions{
		ReviewRunner:       reviewRunner,
		DefaultReviewModel: "gpt-5.3-codex",
		LargerReviewModel:  "gpt-5.3-codex-large",
		MaxRetries:         1,
		AttemptTimeout:     200 * time.Millisecond,
		DecisionLogger: func(_ context.Context, entry ReviewDecisionLog) {
			decisionsMu.Lock()
			decisions = append(decisions, entry)
			decisionsMu.Unlock()
		},
	})

	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		RegistryTTL:    2 * time.Second,
		ServiceHandler: handler.Handle,
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "executor",
		Bus:          bus,
		Runner:       fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() { _ = executor.Start(ctx) }()
	time.Sleep(20 * time.Millisecond)

	response, err := executor.RequestReview(ctx, ServiceRequestPayload{
		RequestID:     "req-1",
		CorrelationID: "corr-1",
		TaskID:        "t-review",
		Metadata: map[string]string{
			"review_policy": "larger_model",
			"prompt":        "Please review this change",
			"repo_root":     "/repo",
		},
	})
	if err != nil {
		t.Fatalf("request review service: %v", err)
	}
	if response.RequestID != "req-1" {
		t.Fatalf("expected request id req-1, got %q", response.RequestID)
	}
	if response.CorrelationID != "corr-1" {
		t.Fatalf("expected correlation id corr-1, got %q", response.CorrelationID)
	}
	if response.ReviewResult == nil {
		t.Fatalf("expected structured review result payload")
	}
	if response.ReviewResult.Verdict != ReviewVerdictFail {
		t.Fatalf("expected verdict fail, got %q", response.ReviewResult.Verdict)
	}
	if response.ReviewResult.BlockingFeedback != "missing tests for retry path" {
		t.Fatalf("expected blocking feedback to round-trip, got %q", response.ReviewResult.BlockingFeedback)
	}
	if response.ReviewResult.SelectedModel != "gpt-5.3-codex-large" {
		t.Fatalf("expected selected model gpt-5.3-codex-large, got %q", response.ReviewResult.SelectedModel)
	}
	if response.ReviewResult.Pass {
		t.Fatalf("expected failed review to report pass=false")
	}

	decisionsMu.Lock()
	defer decisionsMu.Unlock()
	if len(decisions) != 1 {
		t.Fatalf("expected one decision log entry, got %d", len(decisions))
	}
	if decisions[0].SelectedModel != "gpt-5.3-codex-large" {
		t.Fatalf("expected logged selected model gpt-5.3-codex-large, got %q", decisions[0].SelectedModel)
	}
	if decisions[0].Verdict != ReviewVerdictFail {
		t.Fatalf("expected logged verdict fail, got %q", decisions[0].Verdict)
	}
}

func TestMastermindReviewRequestHandlerRetriesDeterministicallyAfterTimeout(t *testing.T) {
	runner := &scriptedReviewRunner{
		runFn: func(attempt int, ctx context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
			if attempt == 1 {
				<-ctx.Done()
				return contracts.RunnerResult{
					Status: contracts.RunnerResultFailed,
					Reason: "timeout",
				}, ctx.Err()
			}
			return contracts.RunnerResult{
				Status: contracts.RunnerResultCompleted,
				Artifacts: map[string]string{
					"review_verdict": "pass",
				},
			}, nil
		},
	}
	handler := NewMastermindReviewRequestHandler(MastermindReviewRequestHandlerOptions{
		ReviewRunner:       runner,
		DefaultReviewModel: "gpt-5.3-codex",
		MaxRetries:         1,
		AttemptTimeout:     20 * time.Millisecond,
	})

	response, err := handler.Handle(context.Background(), ServiceRequestPayload{
		RequestID:     "req-timeout",
		CorrelationID: "corr-timeout",
		TaskID:        "task-timeout",
		Service:       ServiceNameReview,
	})
	if err != nil {
		t.Fatalf("expected handler to recover after retry, got %v", err)
	}
	if runner.attempts() != 2 {
		t.Fatalf("expected deterministic retry count 2, got %d", runner.attempts())
	}
	if response.ReviewResult == nil {
		t.Fatalf("expected review result payload")
	}
	if response.ReviewResult.Verdict != ReviewVerdictPass {
		t.Fatalf("expected final verdict pass, got %q", response.ReviewResult.Verdict)
	}
}

func TestExecutorRequestServiceManagesPendingQueueWithSharedResponseSubscription(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:             "executor",
		Bus:            bus,
		Subjects:       subjects,
		Runner:         fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Capabilities:   []Capability{CapabilityImplement},
		RequestTimeout: 500 * time.Millisecond,
	})

	requestCh, unsubscribe, err := bus.Subscribe(ctx, subjects.ServiceRequest)
	if err != nil {
		t.Fatalf("subscribe service request: %v", err)
	}
	defer unsubscribe()

	type requestEnvelope struct {
		env     EventEnvelope
		payload ServiceRequestPayload
	}
	receivedAll := make(chan []requestEnvelope, 1)
	releaseResponses := make(chan struct{})
	go func() {
		received := make([]requestEnvelope, 0, 3)
		for len(received) < 3 {
			raw := <-requestCh
			payload := ServiceRequestPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			received = append(received, requestEnvelope{env: raw, payload: payload})
		}
		receivedAll <- received
		<-releaseResponses
		for i := len(received) - 1; i >= 0; i-- {
			resp := ServiceResponsePayload{
				RequestID:     received[i].payload.RequestID,
				CorrelationID: received[i].payload.CorrelationID,
				ExecutorID:    received[i].payload.ExecutorID,
				Service:       received[i].payload.Service,
				Artifacts: map[string]string{
					"service": received[i].payload.Service,
					"task_id": received[i].payload.TaskID,
				},
			}
			env, err := NewEventEnvelope(EventTypeServiceResponse, "mastermind", resp.CorrelationID, resp)
			if err != nil {
				continue
			}
			_ = bus.Publish(ctx, subjects.ServiceResult, env)
		}
	}()

	errCh := make(chan error, 3)
	responseCh := make(chan ServiceResponsePayload, 3)
	for _, taskID := range []string{"task-a", "task-b", "task-c"} {
		taskID := taskID
		go func() {
			response, requestErr := executor.RequestService(ctx, ServiceRequestPayload{
				TaskID:   taskID,
				Service:  ServiceNameReview,
				Metadata: map[string]string{"task": taskID},
			})
			if requestErr != nil {
				errCh <- requestErr
				return
			}
			responseCh <- response
		}()
	}

	var received []requestEnvelope
	select {
	case received = <-receivedAll:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for in-flight service requests")
	}

	if got := executor.pendingServiceRequestCount(); got != 3 {
		t.Fatalf("expected 3 pending service requests, got %d", got)
	}
	if got := memorySubscriberCount(bus, subjects.ServiceResult); got != 1 {
		t.Fatalf("expected single shared response subscriber, got %d", got)
	}

	close(releaseResponses)

	results := map[string]string{}
	for i := 0; i < 3; i++ {
		select {
		case err := <-errCh:
			t.Fatalf("unexpected request error: %v", err)
		case response := <-responseCh:
			results[response.Artifacts["task_id"]] = response.Artifacts["service"]
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for service response %d", i+1)
		}
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 routed responses, got %v", results)
	}
	if got := executor.pendingServiceRequestCount(); got != 0 {
		t.Fatalf("expected pending service queue to drain, got %d", got)
	}
	_ = received
}

func TestExecutorRequestServiceTimeoutCleansPendingQueue(t *testing.T) {
	bus := NewMemoryBus()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:             "executor",
		Bus:            bus,
		Subjects:       subjects,
		Runner:         fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Capabilities:   []Capability{CapabilityImplement},
		RequestTimeout: 40 * time.Millisecond,
	})

	_, err := executor.RequestService(context.Background(), ServiceRequestPayload{
		TaskID:  "timeout-task",
		Service: ServiceNameReview,
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if got := executor.pendingServiceRequestCount(); got != 0 {
		t.Fatalf("expected no pending requests after timeout, got %d", got)
	}
}

func memorySubscriberCount(bus *MemoryBus, subject string) int {
	bus.mu.RLock()
	defer bus.mu.RUnlock()
	return len(bus.channels[subject])
}

func TestMastermindReturnsErrorWhenExecutorDisconnects(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := time.Now
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		RegistryTTL:    10 * time.Millisecond,
		RequestTimeout: 80 * time.Millisecond,
		Clock:          clock,
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "executor",
		Bus:          bus,
		Runner:       fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Capabilities: []Capability{CapabilityImplement},
		Clock:        clock,
	})
	go func() { _ = executor.Start(ctx) }()
	time.Sleep(20 * time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	_, err := mastermind.DispatchTask(ctx, TaskDispatchRequest{
		RunnerRequest: contracts.RunnerRequest{TaskID: "disconnect", Mode: contracts.RunnerModeImplement},
	})
	if err == nil {
		t.Fatalf("expected dispatch to fail after executor heartbeat expires")
	}
}

func TestMastermindAcknowledgesTaskStatusUpdateCommand(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statusBackend := &fakeTaskStatusBackend{
		t: t,
	}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": statusBackend,
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

	commandID, err := mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		Backends:  []string{"tk"},
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		Comment:   "done by external",
		AuthToken: "token",
	})
	if err != nil {
		t.Fatalf("publish task status update: %v", err)
	}
	ack := readTaskStatusUpdateAck(t, ackCh)
	if ack.CommandID != commandID {
		t.Fatalf("expected command id %q, got %q", commandID, ack.CommandID)
	}
	if ack.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected status %q, got %q", contracts.TaskStatusClosed, ack.Status)
	}
	if len(ack.Versions) != 1 || ack.Versions["tk"] != 1 {
		t.Fatalf("expected version map with tk=1, got %+v", ack.Versions)
	}
	taskStatus, commentData := statusBackend.status("task-1")
	if len(commentData) == 0 && taskStatus == "" {
		t.Fatalf("expected task status write")
	}
	if taskStatus != contracts.TaskStatusClosed {
		t.Fatalf("expected backend status %q, got %q", contracts.TaskStatusClosed, taskStatus)
	}
	if commentData[inboxStatusCommentKey] != "done by external" {
		t.Fatalf("expected backend comment update, got %q", commentData[inboxStatusCommentKey])
	}
}

func TestMastermindAcknowledgesTaskStatusUpdateAcrossMultipleBackends(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tkBackend := &fakeTaskStatusBackend{t: t}
	linearBackend := &fakeTaskStatusBackend{t: t}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk":     tkBackend,
			"linear": linearBackend,
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

	commandID, err := mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		Comment:   "multi write",
		AuthToken: "token",
	})
	if err != nil {
		t.Fatalf("publish task status update: %v", err)
	}
	ack := readTaskStatusUpdateAck(t, ackCh)
	if ack.CommandID != commandID {
		t.Fatalf("expected command id %q, got %q", commandID, ack.CommandID)
	}
	if len(ack.Versions) != 2 || ack.Versions["tk"] != 1 || ack.Versions["linear"] != 1 {
		t.Fatalf("expected version map for tk/linear with 1, got %+v", ack.Versions)
	}
	if _, commentData := tkBackend.status("task-1"); len(commentData) == 0 {
		t.Fatalf("expected status write for tk backend")
	}
	if _, commentData := linearBackend.status("task-1"); len(commentData) == 0 {
		t.Fatalf("expected status write for linear backend")
	}
}

func TestMastermindRejectsTaskStatusUpdateOnConflict(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statusBackend := &fakeTaskStatusBackend{
		t: t,
	}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": statusBackend,
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
	rejectCh, unsubscribeReject, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateReject)
	if err != nil {
		t.Fatalf("subscribe reject: %v", err)
	}
	defer unsubscribeReject()

	if _, err := mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		Backends:  []string{"tk"},
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		AuthToken: "token",
	}); err != nil {
		t.Fatalf("publish initial update: %v", err)
	}
	_ = readTaskStatusUpdateAck(t, ackCh)
	_, err = mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		Backends:        []string{"tk"},
		TaskID:          "task-1",
		Status:          contracts.TaskStatusInProgress,
		AuthToken:       "token",
		ExpectedVersion: 999,
	})
	if err != nil {
		t.Fatalf("publish conflicting update: %v", err)
	}
	reject := readTaskStatusUpdateReject(t, rejectCh)
	if reject.CommandID == "" {
		t.Fatalf("expected reject command id")
	}
	if !strings.Contains(strings.ToLower(reject.Reason), "version") {
		t.Fatalf("expected version conflict reason, got %q", reject.Reason)
	}
	if got, _ := statusBackend.callsFor("task-1"); got != 1 {
		t.Fatalf("expected only one status write, got %d", got)
	}
}

func TestMastermindRejectsTaskStatusUpdateOnConflictAcrossMultipleBackends(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backendA := &fakeTaskStatusBackend{
		t: t,
	}
	backendB := &fakeTaskStatusBackend{
		t: t,
	}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backendA,
			"gh": backendB,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	ackCh, _, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateAck)
	if err != nil {
		t.Fatalf("subscribe ack: %v", err)
	}
	rejectCh, unsubscribeReject, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateReject)
	if err != nil {
		t.Fatalf("subscribe reject: %v", err)
	}
	defer unsubscribeReject()

	if _, err := mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		AuthToken: "token",
	}); err != nil {
		t.Fatalf("publish initial update: %v", err)
	}
	_ = readTaskStatusUpdateAck(t, ackCh)
	_, err = mastermind.PublishTaskStatusUpdate(ctx, TaskStatusUpdatePayload{
		TaskID:          "task-1",
		Status:          contracts.TaskStatusInProgress,
		AuthToken:       "token",
		ExpectedVersion: 2,
	})
	if err != nil {
		t.Fatalf("publish conflicting update: %v", err)
	}
	reject := readTaskStatusUpdateReject(t, rejectCh)
	if !strings.Contains(strings.ToLower(reject.Reason), "version") {
		t.Fatalf("expected version conflict reason, got %q", reject.Reason)
	}
	if count, _ := backendA.callsFor("task-1"); count != 1 {
		t.Fatalf("expected only one write to tk backend, got %d", count)
	}
	if count, _ := backendB.callsFor("task-1"); count != 1 {
		t.Fatalf("expected only one write to gh backend, got %d", count)
	}
}

func TestMastermindRejectsTaskStatusUpdateWhenAuthTokenMissing(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statusBackend := &fakeTaskStatusBackend{
		t: t,
	}
	subjects := DefaultEventSubjects("unit")
	m := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": statusBackend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := m.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	rejectCh, unsubscribeReject, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateReject)
	if err != nil {
		t.Fatalf("subscribe reject: %v", err)
	}
	defer unsubscribeReject()

	env, err := NewEventEnvelope(EventTypeTaskStatusUpdate, "writer", "cmd-1", TaskStatusUpdatePayload{
		Backends: []string{"tk"},
		TaskID:   "task-1",
		Status:   contracts.TaskStatusClosed,
	})
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskStatusUpdate, env); err != nil {
		t.Fatalf("publish rejected update: %v", err)
	}
	reject := readTaskStatusUpdateReject(t, rejectCh)
	if !strings.Contains(strings.ToLower(reject.Reason), "token") {
		t.Fatalf("expected auth token rejection reason, got %q", reject.Reason)
	}
}

func TestMastermindSubscribeTaskGraphReceivesSnapshotsAndDiffs(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")
	m := NewMastermind(MastermindOptions{
		ID:       "mastermind",
		Bus:      bus,
		Subjects: subjects,
	})
	if err := m.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	graphCh, unsubscribeGraph, err := m.SubscribeTaskGraph(ctx, TaskGraphSubscriptionFilter{Backends: []string{"backend-a"}})
	if err != nil {
		t.Fatalf("subscribe task graph: %v", err)
	}
	defer unsubscribeGraph()

	snapshot := TaskGraphSnapshotPayload{
		Backend: "backend-a",
		RootID:  "task-root",
		TaskTree: contracts.TaskTree{
			Root: contracts.Task{
				ID:     "task-root",
				Status: contracts.TaskStatusOpen,
			},
			Tasks: map[string]contracts.Task{
				"task-root": {
					ID:     "task-root",
					Status: contracts.TaskStatusOpen,
				},
			},
		},
	}
	snapshotEnv, err := NewEventEnvelope(EventTypeTaskGraphSnapshot, "writer", "", snapshot)
	if err != nil {
		t.Fatalf("build snapshot envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskGraphSnapshot, snapshotEnv); err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	event := readTaskGraphEvent(t, graphCh)
	if event.Type != EventTypeTaskGraphSnapshot || event.Snapshot == nil {
		t.Fatalf("expected snapshot event, got %+v", event)
	}
	if event.Snapshot.RootID != "task-root" {
		t.Fatalf("unexpected snapshot root %q", event.Snapshot.RootID)
	}

	diff := TaskGraphDiffPayload{
		Backend: "backend-a",
		RootID:  "task-root",
		Changes: []string{"task-root:status"},
	}
	diffEnv, err := NewEventEnvelope(EventTypeTaskGraphDiff, "writer", "", diff)
	if err != nil {
		t.Fatalf("build diff envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskGraphDiff, diffEnv); err != nil {
		t.Fatalf("publish diff: %v", err)
	}
	diffEvent := readTaskGraphEvent(t, graphCh)
	if diffEvent.Type != EventTypeTaskGraphDiff || diffEvent.Diff == nil {
		t.Fatalf("expected diff event, got %+v", diffEvent)
	}
}

func TestMastermindPublishesTaskGraphSnapshotsAndDiffsFromStatusBackends(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subjects := DefaultEventSubjects("unit")
	graphBackend := &fakeTaskStatusBackend{t: t}
	graphBackend.SetTaskTree("root-1", &contracts.TaskTree{
		Root: contracts.Task{ID: "root-1", Title: "Root", Status: contracts.TaskStatusOpen},
		Tasks: map[string]contracts.Task{
			"root-1": {ID: "root-1", Title: "Root", Status: contracts.TaskStatusOpen},
		},
	})

	mastermind := NewMastermind(MastermindOptions{
		ID:                    "mastermind",
		Bus:                   bus,
		Subjects:              subjects,
		RegistryTTL:           2 * time.Second,
		TaskGraphSyncRoots:    []string{"root-1"},
		TaskGraphSyncInterval: 20 * time.Millisecond,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": graphBackend,
		},
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	graphCh, unsubscribeGraph, err := mastermind.SubscribeTaskGraph(ctx, TaskGraphSubscriptionFilter{Backends: []string{"tk"}})
	if err != nil {
		t.Fatalf("subscribe task graph: %v", err)
	}
	defer unsubscribeGraph()

	snapshot := readTaskGraphEvent(t, graphCh)
	if snapshot.Type != EventTypeTaskGraphSnapshot || snapshot.Snapshot == nil {
		t.Fatalf("expected task-graph snapshot event, got %+v", snapshot)
	}
	if snapshot.Snapshot.RootID != "root-1" {
		t.Fatalf("expected root_id=root-1, got %q", snapshot.Snapshot.RootID)
	}

	graphBackend.SetTaskTree("root-1", &contracts.TaskTree{
		Root: contracts.Task{ID: "root-1", Title: "Root", Status: contracts.TaskStatusClosed},
		Tasks: map[string]contracts.Task{
			"root-1": {ID: "root-1", Title: "Root", Status: contracts.TaskStatusClosed},
		},
	})
	diff := readNextTaskGraphEventOfType(t, graphCh, EventTypeTaskGraphDiff)
	if diff.Diff == nil || len(diff.Diff.Changes) == 0 {
		t.Fatalf("expected non-empty task-graph diff event, got %+v", diff)
	}
}

func readTaskStatusUpdateAck(t *testing.T, ch <-chan EventEnvelope) TaskStatusUpdateAckPayload {
	t.Helper()
	timeout := time.After(1 * time.Second)
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("task status ack channel closed")
			}
			if raw.Type != EventTypeTaskStatusAck {
				continue
			}
			payload := TaskStatusUpdateAckPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal ack: %v", err)
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for task status ack")
		}
	}
}

func readTaskStatusUpdateReject(t *testing.T, ch <-chan EventEnvelope) TaskStatusUpdateRejectPayload {
	t.Helper()
	timeout := time.After(1 * time.Second)
	for {
		select {
		case raw, ok := <-ch:
			if !ok {
				t.Fatalf("task status reject channel closed")
			}
			if raw.Type != EventTypeTaskStatusReject {
				continue
			}
			payload := TaskStatusUpdateRejectPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal reject: %v", err)
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for task status reject")
		}
	}
}

func readTaskGraphEvent(t *testing.T, ch <-chan TaskGraphEvent) TaskGraphEvent {
	t.Helper()
	timeout := time.After(1 * time.Second)
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatalf("task graph event channel closed")
			}
			return event
		case <-timeout:
			t.Fatalf("timed out waiting for task graph event")
		}
	}
}

func readNextTaskGraphEventOfType(t *testing.T, ch <-chan TaskGraphEvent, eventType EventType) TaskGraphEvent {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatalf("task graph event channel closed")
			}
			if event.Type != eventType {
				continue
			}
			return event
		case <-timeout:
			t.Fatalf("timed out waiting for task graph event %q", eventType)
		}
	}
}

type fakeTaskStatusBackend struct {
	t          *testing.T
	mu         sync.Mutex
	taskStatus map[string]contracts.TaskStatus
	data       map[string]map[string]string
	calls      map[string]int
	taskTrees  map[string]*contracts.TaskTree
}

func (b *fakeTaskStatusBackend) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	if b == nil {
		return nil, fmt.Errorf("backend is nil")
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, fmt.Errorf("parent task ID is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskTrees != nil {
		if tree, ok := b.taskTrees[rootID]; ok {
			return cloneTaskTreeForTest(tree), nil
		}
	}
	return &contracts.TaskTree{
		Root: contracts.Task{
			ID:     rootID,
			Status: contracts.TaskStatusOpen,
		},
		Tasks: map[string]contracts.Task{
			rootID: {
				ID:     rootID,
				Status: contracts.TaskStatusOpen,
			},
		},
	}, nil
}

func (b *fakeTaskStatusBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	status, ok := b.taskStatus[taskID]
	if !ok {
		return nil, nil
	}
	return &contracts.Task{ID: taskID, Status: status}, nil
}

func (b *fakeTaskStatusBackend) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	if b == nil {
		return fmt.Errorf("backend is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskStatus == nil {
		b.taskStatus = map[string]contracts.TaskStatus{}
	}
	if b.calls == nil {
		b.calls = map[string]int{}
	}
	b.calls[taskID]++
	b.taskStatus[taskID] = status
	return nil
}

func (b *fakeTaskStatusBackend) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if b == nil {
		return fmt.Errorf("backend is nil")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.data == nil {
		b.data = map[string]map[string]string{}
	}
	sanitized := map[string]string{}
	for key, value := range data {
		sanitized[strings.TrimSpace(key)] = value
	}
	b.data[taskID] = sanitized
	return nil
}

func (b *fakeTaskStatusBackend) status(taskID string) (contracts.TaskStatus, map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskStatus == nil {
		return "", nil
	}
	status := b.taskStatus[taskID]
	data, ok := b.data[taskID]
	if !ok {
		data = map[string]string{}
	}
	return status, data
}

func (b *fakeTaskStatusBackend) SetTaskTree(rootID string, tree *contracts.TaskTree) {
	if b == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.taskTrees == nil {
		b.taskTrees = map[string]*contracts.TaskTree{}
	}
	if rootID == "" {
		return
	}
	if tree == nil {
		delete(b.taskTrees, rootID)
		return
	}
	b.taskTrees[rootID] = cloneTaskTreeForTest(tree)
}

func (b *fakeTaskStatusBackend) callsFor(taskID string) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls == nil {
		return 0, false
	}
	callCount, ok := b.calls[taskID]
	return callCount, ok
}

func cloneTaskTreeForTest(tree *contracts.TaskTree) *contracts.TaskTree {
	if tree == nil {
		return nil
	}
	c := *tree
	c.Root.Metadata = map[string]string{}
	for key, value := range tree.Root.Metadata {
		c.Root.Metadata[strings.TrimSpace(key)] = value
	}
	c.Tasks = map[string]contracts.Task{}
	for taskID, task := range tree.Tasks {
		clonedTask := task
		clonedTask.Metadata = map[string]string{}
		for key, value := range task.Metadata {
			clonedTask.Metadata[strings.TrimSpace(key)] = value
		}
		c.Tasks[taskID] = clonedTask
	}
	if len(tree.Relations) > 0 {
		c.Relations = append([]contracts.TaskRelation{}, tree.Relations...)
	}
	if len(tree.MissingDependencyIDs) > 0 {
		c.MissingDependencyIDs = append([]string{}, tree.MissingDependencyIDs...)
	}
	if len(tree.MissingDependenciesByTask) > 0 {
		c.MissingDependenciesByTask = map[string][]string{}
		for taskID, deps := range tree.MissingDependenciesByTask {
			c.MissingDependenciesByTask[strings.TrimSpace(taskID)] = append([]string{}, deps...)
		}
	}
	return &c
}
