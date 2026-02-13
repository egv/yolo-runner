package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

func TestJSONLQueuePersistsJobContractFields(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "linear-webhook.jobs.jsonl")
	queue := NewJSONLQueue(queuePath)

	payload := readFixture(t, "agent_session_event.created.v1.json")
	event, err := linear.DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	job := buildJob(event, payload, "delivery-1", time.Date(2026, 2, 10, 0, 12, 27, 0, time.UTC))
	if err := queue.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	contents, err := os.ReadFile(queuePath)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	line := bytes.TrimSpace(contents)
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("decode queue JSONL line: %v", err)
	}

	if got, ok := record["contractVersion"].(float64); !ok || int(got) != JobContractVersion1 {
		t.Fatalf("expected contractVersion=%d, got %#v", JobContractVersion1, record["contractVersion"])
	}
	if got, ok := record["sessionId"].(string); !ok || got != "session-1" {
		t.Fatalf("expected sessionId=session-1, got %#v", record["sessionId"])
	}
	if got, ok := record["sessionStep"].(string); !ok || got != "session-1:created" {
		t.Fatalf("expected sessionStep=session-1:created, got %#v", record["sessionStep"])
	}
	if got, ok := record["idempotencyKey"].(string); !ok || got != "linear-agent-session/v1:session-1:created" {
		t.Fatalf("unexpected idempotencyKey: %#v", record["idempotencyKey"])
	}
}
