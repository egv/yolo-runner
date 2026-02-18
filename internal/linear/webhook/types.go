package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

var (
	ErrQueueFull        = errors.New("webhook dispatch queue is full")
	ErrDispatcherClosed = errors.New("webhook dispatcher is closed")
)

const JobContractVersion1 = 1

type Job struct {
	ID              string                         `json:"id"`
	ContractVersion int                            `json:"contractVersion"`
	IdempotencyKey  string                         `json:"idempotencyKey"`
	SessionID       string                         `json:"sessionId"`
	StepAction      linear.AgentSessionEventAction `json:"stepAction"`
	StepID          string                         `json:"stepId"`
	DeliveryID      string                         `json:"deliveryId,omitempty"`
	ReceivedAt      time.Time                      `json:"receivedAt"`
	Event           linear.AgentSessionEvent       `json:"event"`
	Payload         json.RawMessage                `json:"payload"`
}

type Dispatcher interface {
	Dispatch(ctx context.Context, job Job) error
}

type QueueSink interface {
	Enqueue(ctx context.Context, job Job) error
}
