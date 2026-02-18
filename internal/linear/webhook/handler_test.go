package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

type captureDispatcher struct {
	jobs []Job
	err  error
}

func (d *captureDispatcher) Dispatch(_ context.Context, job Job) error {
	d.jobs = append(d.jobs, job)
	return d.err
}

func TestHandlerRejectsNonPost(t *testing.T) {
	h := NewHandler(&captureDispatcher{}, HandlerOptions{})
	req := httptest.NewRequest(http.MethodGet, "/linear/webhook", nil)
	rw := httptest.NewRecorder()

	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected %d, got %d", http.StatusMethodNotAllowed, rw.Code)
	}
}

func TestHandlerRejectsInvalidJSON(t *testing.T) {
	dispatcher := &captureDispatcher{}
	h := NewHandler(dispatcher, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()

	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rw.Code)
	}
	if len(dispatcher.jobs) != 0 {
		t.Fatalf("expected no dispatched jobs, got %d", len(dispatcher.jobs))
	}
}

func TestHandlerRejectsInvalidEventPayload(t *testing.T) {
	dispatcher := &captureDispatcher{}
	h := NewHandler(dispatcher, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(invalidPayloadVersionPayload(t)))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()

	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rw.Code)
	}
	if len(dispatcher.jobs) != 0 {
		t.Fatalf("expected no dispatched jobs, got %d", len(dispatcher.jobs))
	}
}

func TestHandlerAcceptsCreatedEventAndDispatches(t *testing.T) {
	dispatcher := &captureDispatcher{}
	h := NewHandler(dispatcher, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(readFixture(t, "agent_session_event.created.v1.json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Linear-Delivery-ID", "delivery-123")
	rw := httptest.NewRecorder()

	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d body=%q", http.StatusAccepted, rw.Code, rw.Body.String())
	}
	if len(dispatcher.jobs) != 1 {
		t.Fatalf("expected one dispatched job, got %d", len(dispatcher.jobs))
	}
	job := dispatcher.jobs[0]
	if job.Event.Action != linear.AgentSessionEventActionCreated {
		t.Fatalf("expected action created, got %q", job.Event.Action)
	}
	if job.DeliveryID != "delivery-123" {
		t.Fatalf("expected delivery id delivery-123, got %q", job.DeliveryID)
	}
	if job.ReceivedAt.IsZero() {
		t.Fatalf("expected receivedAt to be set")
	}
}

func TestBuildJobDefinesWebhookWorkerContract(t *testing.T) {
	payload := readFixture(t, "agent_session_event.prompted.v1.json")
	event, err := linear.DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}

	now := time.Date(2026, 2, 18, 22, 30, 0, 0, time.UTC)
	job := buildJob(event, payload, "delivery-1", now)

	if job.ContractVersion != JobContractVersion1 {
		t.Fatalf("expected contract version %d, got %d", JobContractVersion1, job.ContractVersion)
	}
	if job.SessionID != "session-1" {
		t.Fatalf("expected sessionId session-1, got %q", job.SessionID)
	}
	if job.StepAction != linear.AgentSessionEventActionPrompted {
		t.Fatalf("expected step action prompted, got %q", job.StepAction)
	}
	if job.StepID != "activity:activity-1" {
		t.Fatalf("expected stepId activity:activity-1, got %q", job.StepID)
	}
	if job.IdempotencyKey != "session-1:prompted:activity:activity-1" {
		t.Fatalf("expected idempotency key session-1:prompted:activity:activity-1, got %q", job.IdempotencyKey)
	}
	if job.ID != "evt-prompted-1" {
		t.Fatalf("expected job ID evt-prompted-1, got %q", job.ID)
	}
}

func TestBuildJobUsesStableIdempotencyForDuplicateDeliveries(t *testing.T) {
	payload := readFixture(t, "agent_session_event.prompted.v1.json")
	event, err := linear.DecodeAgentSessionEvent(payload)
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}
	event.ID = ""

	job1 := buildJob(event, payload, "delivery-a", time.Date(2026, 2, 18, 22, 30, 0, 0, time.UTC))
	job2 := buildJob(event, payload, "delivery-b", time.Date(2026, 2, 18, 22, 31, 0, 0, time.UTC))

	if job1.IdempotencyKey == "" {
		t.Fatal("expected non-empty idempotency key")
	}
	if job1.IdempotencyKey != job2.IdempotencyKey {
		t.Fatalf("expected duplicate deliveries to share idempotency key, got %q and %q", job1.IdempotencyKey, job2.IdempotencyKey)
	}
	if job1.ID != job2.ID {
		t.Fatalf("expected duplicate deliveries to produce same job ID, got %q and %q", job1.ID, job2.ID)
	}
	if job1.DeliveryID == job2.DeliveryID {
		t.Fatalf("expected delivery IDs to differ, got %q", job1.DeliveryID)
	}
}

func TestHandlerMapsQueueFullTo503(t *testing.T) {
	dispatcher := &captureDispatcher{err: ErrQueueFull}
	h := NewHandler(dispatcher, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(readFixture(t, "agent_session_event.created.v1.json")))
	rw := httptest.NewRecorder()

	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d", http.StatusServiceUnavailable, rw.Code)
	}
}

func TestHandlerMapsUnexpectedDispatchFailureTo500(t *testing.T) {
	dispatcher := &captureDispatcher{err: errors.New("boom")}
	h := NewHandler(dispatcher, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(readFixture(t, "agent_session_event.prompted.v1.json")))
	rw := httptest.NewRecorder()

	h.ServeHTTP(rw, req)

	if rw.Code != http.StatusInternalServerError {
		t.Fatalf("expected %d, got %d", http.StatusInternalServerError, rw.Code)
	}
}

func TestHandlerReturns503WhenDispatchTimesOut(t *testing.T) {
	dispatcher := &blockingDispatcher{}
	h := NewHandler(dispatcher, HandlerOptions{
		DispatchTimeout: 20 * time.Millisecond,
	})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(readFixture(t, "agent_session_event.created.v1.json")))
	rw := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rw, req)
	elapsed := time.Since(start)

	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected %d, got %d body=%q", http.StatusServiceUnavailable, rw.Code, rw.Body.String())
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("expected timeout response to be fast, got %s", elapsed)
	}
	if !dispatcher.sawDeadline() {
		t.Fatal("expected dispatcher context to include deadline")
	}
}

func TestHandlerAckRemainsFastWhenSinkIsSlow(t *testing.T) {
	sink := &slowQueueSink{release: make(chan struct{}), started: make(chan struct{}, 1)}
	dispatcher := NewAsyncDispatcher(sink, 8)
	t.Cleanup(func() {
		close(sink.release)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = dispatcher.Close(ctx)
	})
	h := NewHandler(dispatcher, HandlerOptions{})
	req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(readFixture(t, "agent_session_event.created.v1.json")))
	rw := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rw, req)
	elapsed := time.Since(start)

	if rw.Code != http.StatusAccepted {
		t.Fatalf("expected %d, got %d", http.StatusAccepted, rw.Code)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected ACK under 150ms with slow sink, got %s", elapsed)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

func invalidPayloadVersionPayload(t *testing.T) []byte {
	t.Helper()
	doc := map[string]any{}
	err := json.Unmarshal(readFixture(t, "agent_session_event.created.v1.json"), &doc)
	if err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	doc["payloadVersion"] = 999
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}

type slowQueueSink struct {
	started chan struct{}
	release chan struct{}
}

func (s *slowQueueSink) Enqueue(context.Context, Job) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-s.release
	return nil
}

type blockingDispatcher struct {
	mu           sync.Mutex
	sawCtxBounds bool
}

func (d *blockingDispatcher) Dispatch(ctx context.Context, _ Job) error {
	if _, ok := ctx.Deadline(); ok {
		d.mu.Lock()
		d.sawCtxBounds = true
		d.mu.Unlock()
	}
	<-ctx.Done()
	return ctx.Err()
}

func (d *blockingDispatcher) sawDeadline() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sawCtxBounds
}
