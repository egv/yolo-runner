package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// TestDetectServeEventPermissionRequestReturnsFalseForNonPermissionEvents verifies
// that non-permission SSE events are not misclassified as permission requests.
func TestDetectServeEventPermissionRequestReturnsFalseForNonPermissionEvents(t *testing.T) {
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
			req, ok := DetectServeEventPermissionRequest(event)
			if ok {
				t.Fatalf("expected non-permission event for %q, got request %#v", tc.name, req)
			}
			if req != nil {
				t.Fatalf("expected nil request for non-permission event %q, got %#v", tc.name, req)
			}
		})
	}
}

// TestDetectServeEventPermissionRequestDetectsPermissionRequested verifies that
// a permission.requested event is detected and its fields are extracted.
func TestDetectServeEventPermissionRequestDetectsPermissionRequested(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data: `{"type":"permission.requested","properties":{"id":"perm_abc","toolName":"bash","options":[` +
			`{"kind":"allow_once","optionId":"opt_allow_once"},` +
			`{"kind":"allow_always","optionId":"opt_allow_always"},` +
			`{"kind":"reject_once","optionId":"opt_reject_once"}` +
			`]}}`,
	}

	req, ok := DetectServeEventPermissionRequest(event)
	if !ok {
		t.Fatal("expected permission.requested to be detected")
	}
	if req == nil {
		t.Fatal("expected non-nil request for permission.requested")
	}
	if req.ID != "perm_abc" {
		t.Fatalf("expected id %q, got %q", "perm_abc", req.ID)
	}
	if req.ToolName != "bash" {
		t.Fatalf("expected toolName %q, got %q", "bash", req.ToolName)
	}
	if len(req.Options) != 3 {
		t.Fatalf("expected 3 options, got %d", len(req.Options))
	}
}

// TestDetectServeEventPermissionRequestExtractsOptions verifies that
// option kinds and IDs are correctly extracted.
func TestDetectServeEventPermissionRequestExtractsOptions(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data: `{"type":"permission.requested","properties":{"id":"perm_xyz","toolName":"read_file","options":[` +
			`{"kind":"allow_once","optionId":"opt_1"},` +
			`{"kind":"allow_always","optionId":"opt_2"}` +
			`]}}`,
	}

	req, ok := DetectServeEventPermissionRequest(event)
	if !ok {
		t.Fatal("expected detection")
	}
	if req.Options[0].Kind != "allow_once" {
		t.Fatalf("expected kind allow_once, got %q", req.Options[0].Kind)
	}
	if req.Options[0].OptionID != "opt_1" {
		t.Fatalf("expected optionId opt_1, got %q", req.Options[0].OptionID)
	}
	if req.Options[1].Kind != "allow_always" {
		t.Fatalf("expected kind allow_always, got %q", req.Options[1].Kind)
	}
	if req.Options[1].OptionID != "opt_2" {
		t.Fatalf("expected optionId opt_2, got %q", req.Options[1].OptionID)
	}
}

// TestDetectServeEventPermissionRequestReturnsFalseForInvalidJSON verifies that
// malformed event data does not panic and returns non-permission.
func TestDetectServeEventPermissionRequestReturnsFalseForInvalidJSON(t *testing.T) {
	event := contracts.SSEEvent{Event: "event", Data: "not-valid-json"}

	req, ok := DetectServeEventPermissionRequest(event)
	if ok {
		t.Fatalf("expected non-permission for invalid JSON, got request %#v", req)
	}
	if req != nil {
		t.Fatalf("expected nil request for invalid JSON, got %#v", req)
	}
}

// TestDetectServeEventPermissionRequestReturnsFalseForBlankEvent verifies that
// an empty SSE event is not treated as a permission request.
func TestDetectServeEventPermissionRequestReturnsFalseForBlankEvent(t *testing.T) {
	event := contracts.SSEEvent{}

	req, ok := DetectServeEventPermissionRequest(event)
	if ok {
		t.Fatalf("expected non-permission for blank event, got request %#v", req)
	}
	if req != nil {
		t.Fatalf("expected nil request for blank event, got %#v", req)
	}
}

// TestDetectServeEventPermissionRequestHandlesMissingID verifies that a
// permission.requested event with no id field returns false.
func TestDetectServeEventPermissionRequestHandlesMissingID(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"permission.requested","properties":{"toolName":"bash","options":[]}}`,
	}

	req, ok := DetectServeEventPermissionRequest(event)
	if ok {
		t.Fatalf("expected non-permission when id is missing, got %#v", req)
	}
}

// TestRespondServePermissionRequestSendsAllowOnceOption verifies that
// RespondServePermissionRequest POSTs to the permission endpoint with the
// allow_once option ID when it is available.
func TestRespondServePermissionRequestSendsAllowOnceOption(t *testing.T) {
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

	req := &ServeEventPermissionRequest{
		ID:       "perm_123",
		ToolName: "bash",
		Options: []ServePermissionOption{
			{Kind: "reject_once", OptionID: "opt_reject"},
			{Kind: "allow_once", OptionID: "opt_allow_once"},
			{Kind: "allow_always", OptionID: "opt_allow_always"},
		},
	}

	if err := RespondServePermissionRequest(context.Background(), srv.Client(), srv.URL, req); err != nil {
		t.Fatalf("respond permission: %v", err)
	}

	if receivedPath != "/permission/perm_123" {
		t.Fatalf("expected path /permission/perm_123, got %q", receivedPath)
	}
	if receivedBody["optionId"] != "opt_allow_once" {
		t.Fatalf("expected optionId opt_allow_once, got %q", receivedBody["optionId"])
	}
}

// TestRespondServePermissionRequestFallsBackToAllowAlways verifies that when
// allow_once is not available, allow_always is selected.
func TestRespondServePermissionRequestFallsBackToAllowAlways(t *testing.T) {
	var receivedBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := &ServeEventPermissionRequest{
		ID:       "perm_456",
		ToolName: "read_file",
		Options: []ServePermissionOption{
			{Kind: "reject_once", OptionID: "opt_reject"},
			{Kind: "allow_always", OptionID: "opt_allow_always"},
		},
	}

	if err := RespondServePermissionRequest(context.Background(), srv.Client(), srv.URL, req); err != nil {
		t.Fatalf("respond permission: %v", err)
	}

	if receivedBody["optionId"] != "opt_allow_always" {
		t.Fatalf("expected optionId opt_allow_always, got %q", receivedBody["optionId"])
	}
}

// TestRespondServePermissionRequestReturnsErrorWhenNoAllowOptionAvailable verifies
// that an error is returned when no allow option is present.
func TestRespondServePermissionRequestReturnsErrorWhenNoAllowOptionAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req := &ServeEventPermissionRequest{
		ID:       "perm_789",
		ToolName: "bash",
		Options: []ServePermissionOption{
			{Kind: "reject_once", OptionID: "opt_reject"},
		},
	}

	err := RespondServePermissionRequest(context.Background(), srv.Client(), srv.URL, req)
	if err == nil {
		t.Fatal("expected error when no allow option is available")
	}
}

// TestRespondServePermissionRequestReturnsErrorOnServerFailure verifies that
// an HTTP error response is propagated as an error.
func TestRespondServePermissionRequestReturnsErrorOnServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	req := &ServeEventPermissionRequest{
		ID:       "perm_err",
		ToolName: "bash",
		Options: []ServePermissionOption{
			{Kind: "allow_once", OptionID: "opt_allow"},
		},
	}

	err := RespondServePermissionRequest(context.Background(), srv.Client(), srv.URL, req)
	if err == nil {
		t.Fatal("expected error on server failure")
	}
}

// TestRespondServePermissionRequestReturnsErrorForNilRequest verifies that
// a nil permission request returns an error without panicking.
func TestRespondServePermissionRequestReturnsErrorForNilRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request for nil permission request")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := RespondServePermissionRequest(context.Background(), srv.Client(), srv.URL, nil)
	if err == nil {
		t.Fatal("expected error for nil permission request")
	}
}

// TestDetectServeEventPermissionRequestReturnsFalseForEmptyOptions verifies
// that a permission.requested event with an empty options array is detected
// correctly and the resulting options slice is empty.
func TestDetectServeEventPermissionRequestReturnsFalseForEmptyOptions(t *testing.T) {
	event := contracts.SSEEvent{
		Event: "event",
		Data:  `{"type":"permission.requested","properties":{"id":"perm_no_opts","toolName":"bash","options":[]}}`,
	}

	req, ok := DetectServeEventPermissionRequest(event)
	if !ok {
		t.Fatal("expected permission.requested to be detected even with empty options")
	}
	if req == nil {
		t.Fatal("expected non-nil request")
	}
	if len(req.Options) != 0 {
		t.Fatalf("expected zero options, got %d", len(req.Options))
	}
}
