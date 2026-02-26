package distributed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type redisSmokeRunner struct{}

func (r redisSmokeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:      "runner_output",
			Message:   "running distributed smoke",
			Timestamp: time.Now().UTC(),
		})
	}
	return contracts.RunnerResult{
		Status:    contracts.RunnerResultCompleted,
		Artifacts: map[string]string{"transport": "redis"},
	}, nil
}

func TestDistributedSmokeTaskDispatchResultLifecycleOverRedis(t *testing.T) {
	redisServer := miniredis.RunT(t)
	address := "redis://" + redisServer.Addr()

	bus, err := NewRedisBus(address)
	if err != nil {
		t.Fatalf("create redis bus: %v", err)
	}
	t.Cleanup(func() {
		_ = bus.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	subjects := DefaultEventSubjects("redis-smoke")
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
		ID:           "executor-redis-smoke",
		Bus:          bus,
		Runner:       redisSmokeRunner{},
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
			TaskID: "redis-smoke-task",
			Mode:   contracts.RunnerModeImplement,
			Prompt: "smoke",
		},
	})
	if err != nil {
		t.Fatalf("dispatch task over redis bus: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %q", result.Status)
	}
	if result.Artifacts["transport"] != "redis" {
		t.Fatalf("expected redis transport artifact, got %q", result.Artifacts["transport"])
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
			if payload.Event.TaskID != "redis-smoke-task" {
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
