package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// TestDetectServeEventQuestionReturnsFalseForNonQuestionEvents verifies that
// non-question SSE events are not misclassified as questions.
func TestDetectServeEventQuestionReturnsFalseForNonQuestionEvents(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{
			name: "session idle",
			data: `{"type":"session.idle","properties":{}}`,
		},
		{
			name: "session error",
			data: `{"type":"session.error","properties":{"error":"boom"}}`,
		},
		{
			name: "permission requested",
			data: `{"type":"permission.requested","properties":{"id":"perm_abc","toolName":"bash","options":[]}}`,
		},
		{
			name: "message part added",
			data: `{"type":"message.part.added","properties":{"part":{"type":"text","text":"hi"}}}`,
		},
		{
			name: "empty data",
			data: `{}`,
		},
		{
			name: "missing type",
			data: `{"properties":{}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := contracts.SSEEvent{Event: "event", Data: tc.data}
			req, ok := DetectServeEventQuestion(event)
			if ok {
				t.Fatalf("expected non-question event for %q, got question %#v", tc.name, req)
			}
			if req != nil {
				t.Fatalf("expected nil request for non-question event %q, got %#v", tc.name, req)
			}
		})
	}
}

// TestDetectServeEventQuestionDetectsAssistantQuestion verifies that an
// assistant.question event is detected and its fields are extracted.
func TestDetectServeEventQuestionDetectsAssistantQuestion(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"assistant.question","properties":{"id":"q_abc","prompt":"What approach would you like?","context":"implementation details"}}`,
	}

	req, ok := DetectServeEventQuestion(event)
	if !ok {
		t.Fatal("expected assistant.question to be detected")
	}
	if req == nil {
		t.Fatal("expected non-nil question for assistant.question")
	}
	if req.ID != "q_abc" {
		t.Fatalf("expected id %q, got %q", "q_abc", req.ID)
	}
	if req.Prompt != "What approach would you like?" {
		t.Fatalf("expected prompt %q, got %q", "What approach would you like?", req.Prompt)
	}
	if req.Context != "implementation details" {
		t.Fatalf("expected context %q, got %q", "implementation details", req.Context)
	}
}

// TestDetectServeEventQuestionExtractsOptions verifies that question options
// are correctly extracted when present.
func TestDetectServeEventQuestionExtractsOptions(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"assistant.question","properties":{"id":"q_xyz","prompt":"Pick one","options":["option A","option B"]}}`,
	}

	req, ok := DetectServeEventQuestion(event)
	if !ok {
		t.Fatal("expected detection")
	}
	if len(req.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(req.Options))
	}
	if req.Options[0] != "option A" {
		t.Fatalf("expected option A, got %q", req.Options[0])
	}
	if req.Options[1] != "option B" {
		t.Fatalf("expected option B, got %q", req.Options[1])
	}
}

// TestDetectServeEventQuestionHandlesMissingID verifies that an
// assistant.question event with no id field returns false.
func TestDetectServeEventQuestionHandlesMissingID(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"assistant.question","properties":{"prompt":"What next?"}}`,
	}

	req, ok := DetectServeEventQuestion(event)
	if ok {
		t.Fatalf("expected non-question when id is missing, got %#v", req)
	}
}

// TestDetectServeEventQuestionReturnsFalseForInvalidJSON verifies that
// malformed event data does not panic and returns non-question.
func TestDetectServeEventQuestionReturnsFalseForInvalidJSON(t *testing.T) {
	event := contracts.SSEEvent{Event: "event", Data: "not-valid-json"}

	req, ok := DetectServeEventQuestion(event)
	if ok {
		t.Fatalf("expected non-question for invalid JSON, got request %#v", req)
	}
	if req != nil {
		t.Fatalf("expected nil request for invalid JSON, got %#v", req)
	}
}

// TestDetectServeEventQuestionReturnsFalseForBlankEvent verifies that
// an empty SSE event is not treated as a question.
func TestDetectServeEventQuestionReturnsFalseForBlankEvent(t *testing.T) {
	event := contracts.SSEEvent{}

	req, ok := DetectServeEventQuestion(event)
	if ok {
		t.Fatalf("expected non-question for blank event, got request %#v", req)
	}
	if req != nil {
		t.Fatalf("expected nil request for blank event, got %#v", req)
	}
}

// TestRespondServeQuestionSendsAnswerToEndpoint verifies that
// RespondServeQuestion POSTs to the question endpoint with the answer text.
func TestRespondServeQuestionSendsAnswerToEndpoint(t *testing.T) {
	var receivedPath string
	var receivedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := &ServeEventQuestion{
		ID:     "q_123",
		Prompt: "Which approach?",
	}

	if err := RespondServeQuestion(context.Background(), srv.Client(), srv.URL, req, "use approach A"); err != nil {
		t.Fatalf("respond question: %v", err)
	}

	if receivedPath != "/question/q_123" {
		t.Fatalf("expected path /question/q_123, got %q", receivedPath)
	}
	if receivedBody["answer"] != "use approach A" {
		t.Fatalf("expected answer %q, got %q", "use approach A", receivedBody["answer"])
	}
}

// TestRespondServeQuestionReturnsErrorOnServerFailure verifies that
// an HTTP error response is propagated as an error.
func TestRespondServeQuestionReturnsErrorOnServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	req := &ServeEventQuestion{
		ID:     "q_err",
		Prompt: "Something?",
	}

	err := RespondServeQuestion(context.Background(), srv.Client(), srv.URL, req, "my answer")
	if err == nil {
		t.Fatal("expected error on server failure")
	}
}

// TestRespondServeQuestionReturnsErrorForNilQuestion verifies that a nil
// question request returns an error without panicking.
func TestRespondServeQuestionReturnsErrorForNilQuestion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := RespondServeQuestion(context.Background(), srv.Client(), srv.URL, nil, "answer")
	if err == nil {
		t.Fatal("expected error for nil question request")
	}
}
