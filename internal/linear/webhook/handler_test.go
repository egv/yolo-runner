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
