package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

const defaultMaxBodyBytes = int64(1 << 20)
const defaultDispatchTimeout = 5 * time.Second

type HandlerOptions struct {
	MaxBodyBytes    int64
	DispatchTimeout time.Duration
	Now             func() time.Time
}

type handler struct {
	dispatcher      Dispatcher
	maxBodyBytes    int64
	dispatchTimeout time.Duration
	now             func() time.Time
}

func NewHandler(dispatcher Dispatcher, opts HandlerOptions) http.Handler {
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	dispatchTimeout := opts.DispatchTimeout
	if dispatchTimeout <= 0 {
		dispatchTimeout = defaultDispatchTimeout
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	return handler{
		dispatcher:      dispatcher,
		maxBodyBytes:    maxBodyBytes,
		dispatchTimeout: dispatchTimeout,
		now:             nowFn,
	}
}

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if err != nil {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid request body", status)
		return
	}

	event, err := linear.DecodeAgentSessionEvent(body)
	if err != nil {
		http.Error(w, "invalid AgentSessionEvent payload", http.StatusBadRequest)
		return
	}

	job := buildJob(event, body, strings.TrimSpace(r.Header.Get("X-Linear-Delivery-ID")), h.now())
	if h.dispatcher == nil {
		http.Error(w, "dispatcher unavailable", http.StatusServiceUnavailable)
		return
	}
	dispatchCtx, cancel := context.WithTimeout(r.Context(), h.dispatchTimeout)
	defer cancel()
	if err := h.dispatcher.Dispatch(dispatchCtx, job); err != nil {
		switch {
		case errors.Is(err, ErrQueueFull), errors.Is(err, ErrDispatcherClosed):
			http.Error(w, "webhook queue unavailable", http.StatusServiceUnavailable)
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			http.Error(w, "webhook queue unavailable", http.StatusServiceUnavailable)
		default:
			http.Error(w, "failed to enqueue webhook job", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, fmt.Sprintf(`{"status":"accepted","jobId":%q}`+"\n", job.ID))
}

func buildJob(event linear.AgentSessionEvent, payload []byte, deliveryID string, receivedAt time.Time) Job {
	sessionID := normalizeSessionID(event)
	stepID := deriveSessionStepID(event, payload)
	idempotencyKey := fmt.Sprintf("%s:%s:%s", sessionID, event.Action, stepID)
	jobID := strings.TrimSpace(event.ID)
	if jobID == "" {
		jobID = idempotencyKey
	}
	copiedPayload := make([]byte, len(payload))
	copy(copiedPayload, payload)
	return Job{
		ID:              jobID,
		ContractVersion: JobContractVersion1,
		IdempotencyKey:  idempotencyKey,
		SessionID:       sessionID,
		StepAction:      event.Action,
		StepID:          stepID,
		DeliveryID:      deliveryID,
		ReceivedAt:      receivedAt,
		Event:           event,
		Payload:         copiedPayload,
	}
}

func normalizeSessionID(event linear.AgentSessionEvent) string {
	sessionID := strings.TrimSpace(event.AgentSession.ID)
	if sessionID == "" {
		return "unknown-session"
	}
	return sessionID
}

func deriveSessionStepID(event linear.AgentSessionEvent, payload []byte) string {
	if event.Action == linear.AgentSessionEventActionPrompted {
		if event.AgentActivity != nil {
			if activityID := strings.TrimSpace(event.AgentActivity.ID); activityID != "" {
				return "activity:" + activityID
			}
		}
	}

	if eventID := strings.TrimSpace(event.ID); eventID != "" {
		return "event:" + eventID
	}
	if event.AgentActivity != nil {
		if activityID := strings.TrimSpace(event.AgentActivity.ID); activityID != "" {
			return "activity:" + activityID
		}
	}
	if !event.CreatedAt.IsZero() {
		return "createdAt:" + event.CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	sum := sha256.Sum256(payload)
	return "payload:" + hex.EncodeToString(sum[:8])
}
