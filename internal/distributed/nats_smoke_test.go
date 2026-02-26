package distributed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

type natsSmokeRunner struct{}

func (r natsSmokeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:      "runner_output",
			Message:   "running distributed smoke",
			Timestamp: time.Now().UTC(),
		})
	}
	return contracts.RunnerResult{
		Status:    contracts.RunnerResultCompleted,
		Artifacts: map[string]string{"transport": "nats"},
	}, nil
}

func TestDistributedSmokeTaskDispatchResultLifecycleOverNATS(t *testing.T) {
	server := startTestNATSServer(t)

	bus, err := NewNATSBus(server.ClientURL())
	if err != nil {
		t.Fatalf("create nats bus: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	subjects := DefaultEventSubjects("nats-smoke")
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

	monitorCh, unsubscribeMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor events: %v", err)
	}
	t.Cleanup(unsubscribeMonitor)

	executor := NewExecutorWorker(ExecutorWorkerOptions{
		ID:           "executor-nats-smoke",
		Bus:          bus,
		Runner:       natsSmokeRunner{},
		Subjects:     subjects,
		Capabilities: []Capability{CapabilityImplement},
	})
	go func() {
		_ = executor.Start(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := mastermind.Registry().Pick(CapabilityImplement); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	result, err := mastermind.DispatchTask(ctx, TaskDispatchRequest{
		RunnerRequest: contracts.RunnerRequest{
			TaskID: "nats-smoke-task",
			Mode:   contracts.RunnerModeImplement,
			Prompt: "smoke",
		},
	})
	if err != nil {
		t.Fatalf("dispatch task over nats bus: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %q", result.Status)
	}
	if result.Artifacts["transport"] != "nats" {
		t.Fatalf("expected nats transport artifact, got %q", result.Artifacts["transport"])
	}

	foundStarted := false
	foundFinished := false
	timeout := time.After(2 * time.Second)
	for !foundStarted || !foundFinished {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for runner lifecycle monitor events: started=%t finished=%t", foundStarted, foundFinished)
		case raw := <-monitorCh:
			payload := MonitorEventPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			if payload.Event.TaskID != "nats-smoke-task" {
				continue
			}
			switch payload.Event.Type {
			case contracts.EventTypeRunnerStarted:
				foundStarted = true
			case contracts.EventTypeRunnerFinished:
				foundFinished = true
			}
		}
	}
}

func startTestNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()

	server, err := natsserver.NewServer(&natsserver.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		t.Fatalf("create nats server: %v", err)
	}

	go server.Start()
	if !server.ReadyForConnections(2 * time.Second) {
		server.Shutdown()
		t.Fatal("nats server did not become ready")
	}

	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}
