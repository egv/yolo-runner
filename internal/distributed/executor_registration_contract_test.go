package distributed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunner) Run(_ context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	<-r.release
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func TestExecutorRegistrationContractPayloadIncludesDiscoveryFields(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("contract")
	registerCh, unsubscribeRegister, err := bus.Subscribe(ctx, subjects.Register)
	if err != nil {
		t.Fatalf("subscribe register: %v", err)
	}
	defer unsubscribeRegister()

	worker := NewExecutorWorker(ExecutorWorkerOptions{
		ID:                 "exec-contract",
		InstanceID:         "instance-a",
		Hostname:           "host-a",
		Bus:                bus,
		Runner:             fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Subjects:           subjects,
		Capabilities:       []Capability{CapabilityImplement},
		SupportedPipelines: []string{"default"},
		SupportedAgents:    []string{"codex"},
		DeclaredLanguages:  []string{"go", "python"},
		DeclaredFeatures:   []string{"testing", "linting"},
		EnvironmentProbes: ExecutorEnvironmentFeatureProbes{
			HasGo:     true,
			HasGit:    true,
			HasDocker: false,
			OS:        "linux",
			Arch:      "amd64",
		},
		CredentialFlags: map[string]bool{
			"has_env:GITHUB_TOKEN": true,
		},
		ResourceHints: ExecutorResourceHints{
			CPUCores: 8,
			MemGB:    16,
		},
		MaxConcurrency:    3,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	go func() {
		_ = worker.Start(ctx)
	}()

	select {
	case raw := <-registerCh:
		if raw.Type != EventTypeExecutorRegistered {
			t.Fatalf("expected event type %q, got %q", EventTypeExecutorRegistered, raw.Type)
		}
		payload := ExecutorRegistrationPayload{}
		if err := json.Unmarshal(raw.Payload, &payload); err != nil {
			t.Fatalf("unmarshal registration payload: %v", err)
		}
		if payload.ExecutorID != "exec-contract" {
			t.Fatalf("expected executor_id exec-contract, got %q", payload.ExecutorID)
		}
		if payload.InstanceID != "instance-a" {
			t.Fatalf("expected instance_id instance-a, got %q", payload.InstanceID)
		}
		if payload.Hostname != "host-a" {
			t.Fatalf("expected hostname host-a, got %q", payload.Hostname)
		}
		if payload.CapabilitySchemaVersion != CapabilitySchemaVersionV1 {
			t.Fatalf("expected capability schema version %q, got %q", CapabilitySchemaVersionV1, payload.CapabilitySchemaVersion)
		}
		if len(payload.SupportedPipelines) != 1 || payload.SupportedPipelines[0] != "default" {
			t.Fatalf("unexpected supported pipelines: %#v", payload.SupportedPipelines)
		}
		if len(payload.SupportedAgents) != 1 || payload.SupportedAgents[0] != "codex" {
			t.Fatalf("unexpected supported agents: %#v", payload.SupportedAgents)
		}
		if payload.EnvironmentProbes.OS != "linux" || payload.EnvironmentProbes.Arch != "amd64" {
			t.Fatalf("unexpected environment probes: %#v", payload.EnvironmentProbes)
		}
		if payload.CredentialFlags["has_env:GITHUB_TOKEN"] != true {
			t.Fatalf("expected credential presence flag for GITHUB_TOKEN")
		}
		if payload.ResourceHints.CPUCores != 8 || payload.ResourceHints.MemGB != 16 {
			t.Fatalf("unexpected resource hints: %#v", payload.ResourceHints)
		}
		if payload.MaxConcurrency != 3 {
			t.Fatalf("expected max_concurrency=3, got %d", payload.MaxConcurrency)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for registration event")
	}
}

func TestMastermindRejectsInvalidExecutorCapabilityPayloads(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("contract")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	invalid := ExecutorRegistrationPayload{
		ExecutorID:              "exec-invalid",
		CapabilitySchemaVersion: "999",
		Capabilities:            []Capability{CapabilityImplement},
	}
	env, err := NewEventEnvelope(EventTypeExecutorRegistered, "executor", "invalid-reg", invalid)
	if err != nil {
		t.Fatalf("build registration event: %v", err)
	}
	if err := bus.Publish(ctx, subjects.Register, env); err != nil {
		t.Fatalf("publish invalid registration: %v", err)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mastermind.Registry().IsAvailable("exec-invalid", time.Now().UTC()) {
			t.Fatal("expected invalid registration payload to be rejected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecutorHeartbeatContractIncludesLoadSlotsAndHealth(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subjects := DefaultEventSubjects("contract")
	heartbeatCh, unsubscribeHeartbeat, err := bus.Subscribe(ctx, subjects.Heartbeat)
	if err != nil {
		t.Fatalf("subscribe heartbeat: %v", err)
	}
	defer unsubscribeHeartbeat()

	runner := &blockingRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	worker := NewExecutorWorker(ExecutorWorkerOptions{
		ID:                "exec-heartbeat",
		Bus:               bus,
		Runner:            runner,
		Subjects:          subjects,
		Capabilities:      []Capability{CapabilityImplement},
		MaxConcurrency:    2,
		HeartbeatInterval: 20 * time.Millisecond,
	})
	go func() {
		_ = worker.Start(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	requestRaw, err := requestForTransport(contracts.RunnerRequest{TaskID: "task-1"})
	if err != nil {
		t.Fatalf("encode runner request: %v", err)
	}
	dispatch := TaskDispatchPayload{
		CorrelationID:        "corr-1",
		TaskID:               "task-1",
		TargetExecutorID:     "exec-heartbeat",
		RequiredCapabilities: []Capability{CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-1", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case raw := <-heartbeatCh:
			if raw.Type != EventTypeExecutorHeartbeat {
				continue
			}
			payload := ExecutorHeartbeatPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal heartbeat payload: %v", err)
			}
			if payload.CurrentLoad < 1 {
				continue
			}
			if payload.AvailableSlots != 1 {
				t.Fatalf("expected available_slots=1 when one task is running, got %d", payload.AvailableSlots)
			}
			if payload.HealthStatus != "healthy" {
				t.Fatalf("expected health_status=healthy, got %q", payload.HealthStatus)
			}
			close(runner.release)
			return
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat with load details")
		}
	}
}

func TestExecutorPublishesOfflineOnShutdown(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	subjects := DefaultEventSubjects("contract")
	offlineCh, unsubscribeOffline, err := bus.Subscribe(ctx, subjects.Offline)
	if err != nil {
		t.Fatalf("subscribe offline: %v", err)
	}
	defer unsubscribeOffline()

	worker := NewExecutorWorker(ExecutorWorkerOptions{
		ID:                "exec-offline",
		Bus:               bus,
		Runner:            fakeRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}},
		Subjects:          subjects,
		Capabilities:      []Capability{CapabilityImplement},
		HeartbeatInterval: 50 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = worker.Start(ctx)
	}()

	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done

	select {
	case raw := <-offlineCh:
		if raw.Type != EventTypeExecutorOffline {
			t.Fatalf("expected event type %q, got %q", EventTypeExecutorOffline, raw.Type)
		}
		payload := ExecutorOfflinePayload{}
		if err := json.Unmarshal(raw.Payload, &payload); err != nil {
			t.Fatalf("unmarshal offline payload: %v", err)
		}
		if payload.ExecutorID != "exec-offline" {
			t.Fatalf("unexpected offline payload executor id %q", payload.ExecutorID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for offline event")
	}
}
