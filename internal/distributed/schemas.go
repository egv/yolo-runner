package distributed

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const (
	EventSchemaVersionV1 SchemaVersion = "1"
	EventSchemaVersionV0 SchemaVersion = "0"
)

type SchemaVersion string

type EventType string

const (
	EventTypeExecutorRegistered EventType = "executor_registered"
	EventTypeExecutorHeartbeat  EventType = "executor_heartbeat"
	EventTypeExecutorOffline    EventType = "executor_offline"
	EventTypeTaskDispatch       EventType = "task_dispatch"
	EventTypeTaskResult         EventType = "task_result"
	EventTypeServiceRequest     EventType = "service_request"
	EventTypeServiceResponse    EventType = "service_response"
	EventTypeTaskGraphSnapshot  EventType = "task_graph_snapshot"
	EventTypeTaskGraphDiff      EventType = "task_graph_diff"
	EventTypeTaskStatusUpdate   EventType = "task_status_update"
	EventTypeTaskStatusAck      EventType = "task_status_ack"
	EventTypeTaskStatusReject   EventType = "task_status_reject"
	EventTypeMonitorEvent       EventType = "monitor_event"
)

type Capability string

const (
	CapabilityImplement    Capability = "implement"
	CapabilityReview       Capability = "review"
	CapabilityRewriteTask  Capability = "rewrite_task"
	CapabilityLargerModel  Capability = "larger_model"
	CapabilityServiceProxy Capability = "service_proxy"
)

type EventEnvelope struct {
	SchemaVersion  SchemaVersion   `json:"schema_version"`
	Type           EventType       `json:"type"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	ReplyTo        string          `json:"reply_to,omitempty"`
	Source         string          `json:"source"`
	Timestamp      time.Time       `json:"timestamp"`
	Payload        json.RawMessage `json:"payload,omitempty"`
}

type EventPayload struct {
	SchemaVersion  string                 `json:"schema_version,omitempty"`
	Type           string                 `json:"type"`
	CorrelationID  string                 `json:"correlation_id,omitempty"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
	ReplyTo        string                 `json:"reply_to,omitempty"`
	Source         string                 `json:"source"`
	Timestamp      string                 `json:"timestamp,omitempty"`
	Payload        map[string]interface{} `json:"payload,omitempty"`
}

var eventIDCounter uint64

type ExecutorRegistrationPayload struct {
	ExecutorID   string            `json:"executor_id"`
	Capabilities []Capability      `json:"capabilities"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	StartedAt    time.Time         `json:"started_at"`
}

type ExecutorHeartbeatPayload struct {
	ExecutorID string            `json:"executor_id"`
	SeenAt     time.Time         `json:"seen_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type TaskDispatchPayload struct {
	CorrelationID        string          `json:"correlation_id"`
	TaskID               string          `json:"task_id"`
	TargetExecutorID     string          `json:"target_executor_id,omitempty"`
	RequiredCapabilities []Capability    `json:"required_capabilities"`
	Request              json.RawMessage `json:"request"`
}

type TaskResultPayload struct {
	CorrelationID string                 `json:"correlation_id"`
	ExecutorID    string                 `json:"executor_id"`
	Result        contracts.RunnerResult `json:"result"`
	Error         string                 `json:"error,omitempty"`
}

type ServiceRequestPayload struct {
	RequestID     string            `json:"request_id"`
	CorrelationID string            `json:"correlation_id"`
	ExecutorID    string            `json:"executor_id"`
	TaskID        string            `json:"task_id"`
	Service       string            `json:"service"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type ServiceResponsePayload struct {
	RequestID     string            `json:"request_id"`
	CorrelationID string            `json:"correlation_id"`
	ExecutorID    string            `json:"executor_id"`
	Service       string            `json:"service"`
	Artifacts     map[string]string `json:"artifacts,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type TaskGraphSnapshotPayload struct {
	Backend  string             `json:"backend"`
	RootID   string             `json:"root_id"`
	TaskTree contracts.TaskTree `json:"task_tree"`
	Metadata map[string]string  `json:"metadata,omitempty"`
}

type TaskGraphDiffPayload struct {
	Backend  string            `json:"backend"`
	RootID   string            `json:"root_id"`
	Changes  []string          `json:"changes"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type TaskStatusUpdatePayload struct {
	CommandID       string               `json:"command_id"`
	Backends        []string             `json:"backends,omitempty"`
	TaskID          string               `json:"task_id"`
	Status          contracts.TaskStatus `json:"status"`
	Comment         string               `json:"comment,omitempty"`
	Metadata        map[string]string    `json:"metadata,omitempty"`
	ExpectedVersion int64                `json:"expected_version,omitempty"`
	AuthToken       string               `json:"auth_token,omitempty"`
}

type TaskStatusUpdateResultPayload struct {
	CommandID string               `json:"command_id"`
	TaskID    string               `json:"task_id"`
	Status    contracts.TaskStatus `json:"status"`
	Backends  []string             `json:"backends,omitempty"`
	Versions  map[string]int64     `json:"versions"`
	Result    string               `json:"result"`
	Message   string               `json:"message,omitempty"`
}

type TaskStatusUpdateAckPayload struct {
	TaskStatusUpdateResultPayload
}

type TaskStatusUpdateRejectPayload struct {
	TaskStatusUpdateResultPayload
	Reason string `json:"reason"`
}

type MonitorEventPayload struct {
	Event contracts.Event `json:"event"`
}

type TaskGraphSubscriptionFilter struct {
	Backends []string
	RootIDs  []string
}

type TaskGraphEvent struct {
	Type     EventType                 `json:"type"`
	Snapshot *TaskGraphSnapshotPayload `json:"snapshot,omitempty"`
	Diff     *TaskGraphDiffPayload     `json:"diff,omitempty"`
}

func canonicalTaskStatusUpdatesBackends(backends []string) []string {
	out := make([]string, 0, len(backends))
	seen := map[string]struct{}{}
	for _, backend := range backends {
		normalized := strings.ToLower(strings.TrimSpace(backend))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func NewEventEnvelope(typ EventType, source string, correlationID string, payload any) (EventEnvelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return EventEnvelope{}, fmt.Errorf("marshal payload: %w", err)
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID = nextEventID()
	}
	idempotencyKey := nextEventID()
	return EventEnvelope{
		SchemaVersion:  EventSchemaVersionV1,
		Type:           typ,
		CorrelationID:  correlationID,
		IdempotencyKey: idempotencyKey,
		Source:         strings.TrimSpace(source),
		Timestamp:      time.Now().UTC(),
		Payload:        raw,
	}, nil
}

func ParseEventEnvelope(raw []byte) (EventEnvelope, error) {
	var evt EventEnvelope
	if err := json.Unmarshal(raw, &evt); err == nil && evt.Type != "" {
		if strings.TrimSpace(string(evt.SchemaVersion)) == "" {
			evt.SchemaVersion = EventSchemaVersionV0
		}
		if strings.TrimSpace(evt.CorrelationID) == "" {
			evt.CorrelationID = nextEventID()
		}
		if strings.TrimSpace(evt.IdempotencyKey) == "" {
			evt.IdempotencyKey = nextEventID()
		}
		return evt, nil
	}

	var legacy struct {
		Type         EventType       `json:"type"`
		Source       string          `json:"source"`
		Correlation  string          `json:"correlation_id"`
		Schema       SchemaVersion   `json:"schema_version"`
		Timestamp    time.Time       `json:"timestamp"`
		TS           string          `json:"ts"`
		Payload      json.RawMessage `json:"payload"`
		EventPayload map[string]any  `json:"event"`
		Data         json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return EventEnvelope{}, err
	}
	if legacy.Type == "" {
		return EventEnvelope{}, fmt.Errorf("missing event type")
	}
	payload := legacy.Payload
	if len(payload) == 0 {
		payload = legacy.Data
	}
	parsed := EventEnvelope{
		SchemaVersion: legacy.Schema,
		Type:          legacy.Type,
		CorrelationID: legacy.Correlation,
		Source:        legacy.Source,
		Timestamp:     legacy.Timestamp,
		Payload:       payload,
	}
	if strings.TrimSpace(parsed.CorrelationID) == "" {
		parsed.CorrelationID = nextEventID()
	}
	if strings.TrimSpace(parsed.IdempotencyKey) == "" {
		parsed.IdempotencyKey = nextEventID()
	}
	if parsed.SchemaVersion == "" {
		parsed.SchemaVersion = EventSchemaVersionV0
	}
	if parsed.Timestamp.IsZero() && strings.TrimSpace(legacy.TS) != "" {
		if parsedTS, err := time.Parse(time.RFC3339, legacy.TS); err == nil {
			parsed.Timestamp = parsedTS
		}
	}
	return parsed, nil
}

func nextEventID() string {
	seq := atomic.AddUint64(&eventIDCounter, 1)
	return fmt.Sprintf("evt-%d-%d", time.Now().UTC().UnixNano(), seq)
}
