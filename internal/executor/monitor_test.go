package executor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRunWithMonitoringEmitsHeartbeatAndProgressEvents(t *testing.T) {
	runner := &monitorTestRunner{
		delay: 50 * time.Millisecond,
		result: contracts.RunnerResult{
			Status: contracts.RunnerResultCompleted,
		},
		progress: []contracts.RunnerProgress{
			{Type: "runner_output", Message: "line output", Metadata: map[string]string{"stream": "stdout"}},
		},
	}
	sink := &monitorRecordingSink{}

	result, err := RunWithMonitoring(context.Background(), runner, sink, contracts.RunnerRequest{
		TaskID: "t-1",
		Mode:   contracts.RunnerModeImplement,
	}, MonitorEventContext{
		TaskID:    "t-1",
		TaskTitle: "Task 1",
		WorkerID:  "worker-1",
		ClonePath: "/tmp/clone",
		QueuePos:  2,
	}, MonitorOptions{
		HeartbeatInterval:    5 * time.Millisecond,
		NoOutputWarningAfter: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run with monitoring failed: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %s", result.Status)
	}
	events := sink.Events()

	if got := eventsByType(events, contracts.EventTypeRunnerHeartbeat); len(got) == 0 {
		t.Fatalf("expected heartbeat events")
	}
	outputs := eventsByType(events, contracts.EventTypeRunnerOutput)
	if len(outputs) != 1 {
		t.Fatalf("expected one runner output event, got %d", len(outputs))
	}
	if outputs[0].TaskID != "t-1" || outputs[0].TaskTitle != "Task 1" || outputs[0].WorkerID != "worker-1" || outputs[0].ClonePath != "/tmp/clone" || outputs[0].QueuePos != 2 {
		t.Fatalf("output event did not preserve monitor context: %#v", outputs[0])
	}
	if outputs[0].Message != "line output" || outputs[0].Metadata["stream"] != "stdout" {
		t.Fatalf("unexpected output event payload: %#v", outputs[0])
	}
}

func TestRunWithMonitoringPreservesAgentBlockedReasonAndDetail(t *testing.T) {
	runner := &monitorTestRunner{
		result: contracts.RunnerResult{Status: contracts.RunnerResultBlocked},
		progress: []contracts.RunnerProgress{
			{
				Type:    string(contracts.EventTypeAgentBlocked),
				Message: "Claude requested permissions for Bash, but you haven't granted them.",
				Metadata: map[string]string{
					"reason": string(contracts.BlockReasonPermissionDenied),
					"detail": "Claude requested permissions for Bash, but you haven't granted them.",
				},
			},
		},
	}
	sink := &monitorRecordingSink{}

	_, err := RunWithMonitoring(context.Background(), runner, sink, contracts.RunnerRequest{
		TaskID: "t-1",
		Mode:   contracts.RunnerModeImplement,
	}, MonitorEventContext{TaskID: "t-1"}, MonitorOptions{
		HeartbeatInterval:    time.Hour,
		NoOutputWarningAfter: time.Hour,
	})
	if err != nil {
		t.Fatalf("run with monitoring failed: %v", err)
	}

	blocked := eventsByType(sink.Events(), contracts.EventTypeAgentBlocked)
	if len(blocked) != 1 {
		t.Fatalf("expected one agent_blocked event, got %d", len(blocked))
	}
	if blocked[0].Reason != contracts.BlockReasonPermissionDenied {
		t.Fatalf("reason = %q; want %q", blocked[0].Reason, contracts.BlockReasonPermissionDenied)
	}
	if blocked[0].Detail == "" {
		t.Fatalf("expected detail to be set: %#v", blocked[0])
	}
}

func TestRunWithMonitoringPreservesCanonicalRunnerProgressTypes(t *testing.T) {
	runner := &monitorTestRunner{
		result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted},
		progress: []contracts.RunnerProgress{
			{Type: string(contracts.EventTypeAgentText), Message: "Exploring"},
			{Type: string(contracts.EventTypeCommandRun), Message: "go test ./internal/opencode/"},
			{Type: string(contracts.EventTypeToolInvoked), Message: "Read README.md"},
			{Type: string(contracts.EventTypeTokenUsage), Message: "usage"},
		},
	}
	sink := &monitorRecordingSink{}

	_, err := RunWithMonitoring(context.Background(), runner, sink, contracts.RunnerRequest{
		TaskID: "t-1",
		Mode:   contracts.RunnerModeImplement,
	}, MonitorEventContext{TaskID: "t-1"}, MonitorOptions{
		HeartbeatInterval:    time.Hour,
		NoOutputWarningAfter: time.Hour,
	})
	if err != nil {
		t.Fatalf("run with monitoring failed: %v", err)
	}

	for _, eventType := range []contracts.EventType{
		contracts.EventTypeAgentText,
		contracts.EventTypeCommandRun,
		contracts.EventTypeToolInvoked,
		contracts.EventTypeTokenUsage,
	} {
		if got := eventsByType(sink.Events(), eventType); len(got) != 1 {
			t.Fatalf("expected one %s event, got %d; events=%#v", eventType, len(got), sink.Events())
		}
	}
	if got := eventsByType(sink.Events(), contracts.EventTypeRunnerProgress); len(got) != 0 {
		t.Fatalf("canonical events must not be collapsed to runner_progress: %#v", got)
	}
}

type monitorTestRunner struct {
	delay    time.Duration
	result   contracts.RunnerResult
	progress []contracts.RunnerProgress
}

func (r *monitorTestRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	for _, progress := range r.progress {
		request.OnProgress(progress)
	}
	return r.result, nil
}

type monitorRecordingSink struct {
	mu     sync.Mutex
	events []contracts.Event
}

func (s *monitorRecordingSink) Emit(_ context.Context, event contracts.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *monitorRecordingSink) Events() []contracts.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]contracts.Event, len(s.events))
	copy(events, s.events)
	return events
}

func eventsByType(events []contracts.Event, eventType contracts.EventType) []contracts.Event {
	matching := []contracts.Event{}
	for _, event := range events {
		if event.Type == eventType {
			matching = append(matching, event)
		}
	}
	return matching
}
