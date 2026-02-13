package webhook

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

const defaultMaxBodyBytes = int64(1 << 20)

type HandlerOptions struct {
	MaxBodyBytes int64
	Now          func() time.Time
}

type handler struct {
	dispatcher   Dispatcher
	maxBodyBytes int64
	now          func() time.Time
}

func NewHandler(dispatcher Dispatcher, opts HandlerOptions) http.Handler {
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	return handler{
		dispatcher:   dispatcher,
		maxBodyBytes: maxBodyBytes,
		now:          nowFn,
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
	if err := h.dispatcher.Dispatch(r.Context(), job); err != nil {
		switch {
		case errors.Is(err, ErrQueueFull), errors.Is(err, ErrDispatcherClosed):
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
	sessionStep := buildSessionStep(event, payload)
	idempotencyKey := buildIdempotencyKey(sessionStep)
	jobID := strings.TrimSpace(event.ID)
	if jobID == "" {
		jobID = idempotencyKey
	}
	copiedPayload := make([]byte, len(payload))
	copy(copiedPayload, payload)
	return Job{
		ContractVersion: JobContractVersion1,
		ID:              jobID,
		DeliveryID:      deliveryID,
		ReceivedAt:      receivedAt,
		SessionID:       sessionStepSessionID(event, payload),
		SessionStep:     sessionStep,
		IdempotencyKey:  idempotencyKey,
		Event:           event,
		Payload:         copiedPayload,
	}
}
