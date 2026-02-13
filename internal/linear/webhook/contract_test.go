package webhook

import (
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

func TestBuildJobAssignsContractForCreatedEvent(t *testing.T) {
	payload := readFixture(t, "agent_session_event.created.v1.json")
	event, err := linear.DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	job := buildJob(event, payload, "delivery-1", time.Date(2026, 2, 10, 0, 12, 27, 0, time.UTC))

	if job.ContractVersion != JobContractVersion1 {
		t.Fatalf("expected contract version %d, got %d", JobContractVersion1, job.ContractVersion)
	}
	if job.SessionID != "session-1" {
		t.Fatalf("expected session id session-1, got %q", job.SessionID)
	}
	if job.SessionStep != "session-1:created" {
		t.Fatalf("expected session step session-1:created, got %q", job.SessionStep)
	}
	if job.IdempotencyKey != "linear-agent-session/v1:session-1:created" {
		t.Fatalf("unexpected idempotency key: %q", job.IdempotencyKey)
	}
}

func TestBuildJobUsesStableIdempotencyForDuplicatePromptedDeliveries(t *testing.T) {
	payload := readFixture(t, "agent_session_event.prompted.v1.json")
	event, err := linear.DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	first := buildJob(event, payload, "delivery-1", time.Date(2026, 2, 10, 0, 16, 13, 0, time.UTC))

	retry := event
	retry.ID = "evt-prompted-1-retry"
	second := buildJob(retry, payload, "delivery-2", time.Date(2026, 2, 10, 0, 16, 15, 0, time.UTC))

	if first.SessionStep != second.SessionStep {
		t.Fatalf("expected duplicate session step to match, got %q vs %q", first.SessionStep, second.SessionStep)
	}
	if first.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("expected duplicate idempotency key to match, got %q vs %q", first.IdempotencyKey, second.IdempotencyKey)
	}
}

func TestBuildJobDifferentiatesPromptedStepsByActivityID(t *testing.T) {
	payload := readFixture(t, "agent_session_event.prompted.v1.json")
	event, err := linear.DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if event.AgentActivity == nil {
		t.Fatal("expected prompted fixture to include agentActivity")
	}

	first := buildJob(event, payload, "delivery-1", time.Date(2026, 2, 10, 0, 16, 13, 0, time.UTC))

	secondEvent := event
	activity := *event.AgentActivity
	activity.ID = "activity-2"
	secondEvent.AgentActivity = &activity
	second := buildJob(secondEvent, payload, "delivery-2", time.Date(2026, 2, 10, 0, 17, 13, 0, time.UTC))

	if first.SessionStep == second.SessionStep {
		t.Fatalf("expected different session steps for different prompted activities, both were %q", first.SessionStep)
	}
	if first.IdempotencyKey == second.IdempotencyKey {
		t.Fatalf("expected different idempotency keys for different prompted activities, both were %q", first.IdempotencyKey)
	}
}
