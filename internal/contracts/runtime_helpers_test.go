package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFinalizeRunErrorUsesContextOutcome(t *testing.T) {
	t.Run("propagates canceled context when runner returns nil", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := FinalizeRunError(ctx, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	})

	t.Run("normalizes wrapped deadline exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		time.Sleep(time.Millisecond)

		err := FinalizeRunError(ctx, errors.New("runner failed"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	})
}

func TestBuildRunnerArtifactsIncludesCommonFieldsAndExtras(t *testing.T) {
	started := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Minute)

	artifacts := BuildRunnerArtifacts("codex", RunnerRequest{
		Model:    "openai/gpt-5.3-codex",
		Mode:     RunnerModeReview,
		Metadata: map[string]string{"clone_path": "/tmp/task-clone"},
	}, RunnerResult{
		Status:     RunnerResultBlocked,
		Reason:     "runner timeout after 10m0s",
		LogPath:    "/tmp/run.jsonl",
		StartedAt:  started,
		FinishedAt: finished,
	}, map[string]string{
		"review_verdict": "fail",
	})

	expected := map[string]string{
		"backend":        "codex",
		"status":         "blocked",
		"model":          "openai/gpt-5.3-codex",
		"mode":           "review",
		"log_path":       "/tmp/run.jsonl",
		"started_at":     "2026-03-18T10:00:00Z",
		"finished_at":    "2026-03-18T10:02:00Z",
		"reason":         "runner timeout after 10m0s",
		"clone_path":     "/tmp/task-clone",
		"review_verdict": "fail",
	}
	if !reflect.DeepEqual(artifacts, expected) {
		t.Fatalf("unexpected artifacts:\nwant: %#v\ngot:  %#v", expected, artifacts)
	}
}

func TestNewRunnerOutputProgressNormalizesOutput(t *testing.T) {
	progress, ok := NewRunnerOutputProgress("stderr", " warn line \x00 ", time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatalf("expected progress event")
	}
	if progress.Type != "runner_output" {
		t.Fatalf("expected runner_output type, got %q", progress.Type)
	}
	if progress.Message != "stderr: warn line" {
		t.Fatalf("unexpected message %q", progress.Message)
	}
	if progress.Timestamp.Format(time.RFC3339) != "2026-03-18T11:00:00Z" {
		t.Fatalf("unexpected timestamp %s", progress.Timestamp.Format(time.RFC3339))
	}
	if progress.Metadata["source"] != "stderr" {
		t.Fatalf("expected stderr source metadata, got %#v", progress.Metadata)
	}

	if _, ok := NewRunnerOutputProgress("stdout", " \n ", time.Now().UTC()); ok {
		t.Fatalf("expected empty line to be ignored")
	}
}

func TestBackendLogSidecarPathSupportsStderrAndProtocolTrace(t *testing.T) {
	logPath := "/tmp/runner/task-1.jsonl"
	if got := BackendLogSidecarPath(logPath, BackendLogStderr); got != "/tmp/runner/task-1.stderr.log" {
		t.Fatalf("unexpected stderr path %q", got)
	}
	if got := BackendLogSidecarPath(logPath, BackendLogProtocolTrace); got != "/tmp/runner/task-1.protocol.log" {
		t.Fatalf("unexpected protocol path %q", got)
	}
}

func TestBackendLogSidecarPathReturnsEmptyForUnsetLogPath(t *testing.T) {
	if got := BackendLogSidecarPath("", BackendLogStderr); got != "" {
		t.Fatalf("expected empty stderr sidecar path, got %q", got)
	}
	if got := BackendLogSidecarPath(" \t ", BackendLogProtocolTrace); got != "" {
		t.Fatalf("expected empty protocol sidecar path, got %q", got)
	}
}

func TestReadinessChecksSupportHTTPAndStdio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ready") != "true" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := CheckHTTPReadiness(context.Background(), server.Client(), HTTPReadinessCheck{
		Endpoint: server.URL,
		Method:   http.MethodGet,
		Headers:  map[string]string{"X-Ready": "true"},
	}); err != nil {
		t.Fatalf("expected healthy endpoint, got %v", err)
	}

	runCalls := 0
	err := CheckStdioReadiness(context.Background(), StdioReadinessCheck{
		Command: "agent --health",
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			runCalls++
			if name != "agent" || strings.Join(args, " ") != "--health" {
				t.Fatalf("unexpected command invocation %q %q", name, args)
			}
			return []byte("ok"), nil
		},
	})
	if err != nil {
		t.Fatalf("expected healthy stdio command, got %v", err)
	}
	if runCalls != 1 {
		t.Fatalf("expected one stdio invocation, got %d", runCalls)
	}
}

func TestFakeStdioJSONRPCHarnessExchangesMessages(t *testing.T) {
	harness := NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	stdin, stdout := harness.ClientIO()

	type envelope struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      int            `json:"id,omitempty"`
		Method  string         `json:"method,omitempty"`
		Params  map[string]any `json:"params,omitempty"`
		Result  map[string]any `json:"result,omitempty"`
	}

	errCh := make(chan error, 1)
	go func() {
		enc := json.NewEncoder(stdin)
		errCh <- enc.Encode(envelope{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "initialize",
			Params:  map[string]any{"client": "test"},
		})
	}()

	msg, err := harness.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if msg.Method != "initialize" {
		t.Fatalf("expected initialize method, got %#v", msg)
	}
	if client := msg.Params["client"]; client != "test" {
		t.Fatalf("expected client=test, got %#v", msg.Params)
	}

	if err := harness.SendMessage(JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result:  map[string]any{"protocolVersion": 1},
	}); err != nil {
		t.Fatalf("send response: %v", err)
	}
	if err := harness.SendMessage(JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/updated",
		Params:  map[string]any{"status": "running"},
	}); err != nil {
		t.Fatalf("send notification: %v", err)
	}

	dec := json.NewDecoder(stdout)
	var response envelope
	if err := dec.Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if version := response.Result["protocolVersion"]; version != float64(1) {
		t.Fatalf("expected protocolVersion=1, got %#v", response.Result)
	}

	var notification envelope
	if err := dec.Decode(&notification); err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if notification.Method != "session/updated" {
		t.Fatalf("expected session/updated notification, got %#v", notification)
	}
	if status := notification.Params["status"]; status != "running" {
		t.Fatalf("expected status=running, got %#v", notification.Params)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("write request: %v", err)
	}
}

func TestFakeStdioJSONRPCHarnessReadMessageHandlesBurstWrites(t *testing.T) {
	harness := NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	stdin, _ := harness.ClientIO()

	burst := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"client":"test"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"sequence":2}}`,
		"",
	}, "\n")
	if _, err := io.WriteString(stdin, burst); err != nil {
		t.Fatalf("write burst: %v", err)
	}

	first, err := harness.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("read first message: %v", err)
	}
	if first.Method != "initialize" {
		t.Fatalf("expected initialize method, got %#v", first)
	}
	if first.Params["client"] != "test" {
		t.Fatalf("expected first client=test, got %#v", first.Params)
	}

	second, err := harness.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("read second message: %v", err)
	}
	if second.Method != "ping" {
		t.Fatalf("expected ping method, got %#v", second)
	}
	if second.Params["sequence"] != float64(2) {
		t.Fatalf("expected second sequence=2, got %#v", second.Params)
	}
}

func TestFakeStdioJSONRPCHarnessReadMessageCancellationDoesNotConsumeNextMessage(t *testing.T) {
	harness := NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	stdin, _ := harness.ClientIO()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := harness.ReadMessage(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled read to return context.Canceled, got %v", err)
	}

	if _, err := io.WriteString(stdin, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"client\":\"test\"}}\n"); err != nil {
		t.Fatalf("write message: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()

	msg, err := harness.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("expected next read to receive queued message, got %v", err)
	}
	if msg.Method != "initialize" {
		t.Fatalf("expected initialize method, got %#v", msg)
	}
	if msg.Params["client"] != "test" {
		t.Fatalf("expected client=test, got %#v", msg.Params)
	}
}

func TestFakeHTTPSSEHarnessSupportsHealthJSONAndSSE(t *testing.T) {
	harness := NewFakeHTTPSSEHarness()
	t.Cleanup(harness.Close)

	harness.SetHealthStatus(http.StatusNoContent)
	harness.QueueJSONResponse("/session", http.StatusCreated, map[string]any{"id": "session-1"})

	if err := CheckHTTPReadiness(context.Background(), harness.Client(), HTTPReadinessCheck{
		Endpoint: harness.HealthURL(),
	}); err != nil {
		t.Fatalf("health check: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, harness.URL("/session"), strings.NewReader(`{"prompt":"ship it"}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := harness.Client().Do(req)
	if err != nil {
		t.Fatalf("post session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["id"] != "session-1" {
		t.Fatalf("expected session id, got %#v", body)
	}

	recorded := harness.Requests("/session")
	if len(recorded) != 1 {
		t.Fatalf("expected one session request, got %#v", recorded)
	}
	if strings.TrimSpace(string(recorded[0].Body)) != `{"prompt":"ship it"}` {
		t.Fatalf("unexpected request body %q", string(recorded[0].Body))
	}

	streamReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, harness.SSEURL(), http.NoBody)
	if err != nil {
		t.Fatalf("build sse request: %v", err)
	}
	streamResp, err := harness.Client().Do(streamReq)
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	defer streamResp.Body.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- harness.SendSSE(SSEEvent{
			Event: "message",
			Data:  `{"type":"token","text":"hello"}`,
		})
	}()

	payload, err := io.ReadAll(io.LimitReader(streamResp.Body, int64(len("event: message\ndata: {\"type\":\"token\",\"text\":\"hello\"}\n\n"))))
	if err != nil {
		t.Fatalf("read sse stream: %v", err)
	}
	if string(payload) != "event: message\ndata: {\"type\":\"token\",\"text\":\"hello\"}\n\n" {
		t.Fatalf("unexpected sse payload %q", string(payload))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send sse event: %v", err)
	}
}

func TestAppendOutputEntryAppendsAndTrimsAtCap(t *testing.T) {
	t.Run("appends entries below cap", func(t *testing.T) {
		var buf []OutputEntry
		buf = appendOutputEntry(buf, OutputEntry{Kind: OutputEntryKindText, Content: "hello"})
		if len(buf) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(buf))
		}
		if buf[0].Content != "hello" {
			t.Fatalf("unexpected content %q", buf[0].Content)
		}
	})

	t.Run("trims oldest entries once cap is reached", func(t *testing.T) {
		var buf []OutputEntry
		for i := range outputBufCap {
			buf = appendOutputEntry(buf, OutputEntry{Kind: OutputEntryKindText, Content: fmt.Sprintf("line-%d", i)})
		}
		if len(buf) != outputBufCap {
			t.Fatalf("expected %d entries at cap, got %d", outputBufCap, len(buf))
		}

		buf = appendOutputEntry(buf, OutputEntry{Kind: OutputEntryKindText, Content: "overflow"})
		if len(buf) != outputBufCap {
			t.Fatalf("expected cap %d after overflow, got %d", outputBufCap, len(buf))
		}
		if buf[len(buf)-1].Content != "overflow" {
			t.Fatalf("expected last entry to be overflow, got %q", buf[len(buf)-1].Content)
		}
		if buf[0].Content != "line-1" {
			t.Fatalf("expected oldest entry trimmed, first entry should be line-1 got %q", buf[0].Content)
		}
	})
}

func TestFakeHTTPSSEHarnessSendSSEReturnsWhenClientBacksUp(t *testing.T) {
	harness := NewFakeHTTPSSEHarness()
	t.Cleanup(harness.Close)

	blocked := make(chan string, 1)
	blocked <- "buffered"
	ready := make(chan string, 1)

	harness.mu.Lock()
	harness.sseClients[blocked] = struct{}{}
	harness.sseClients[ready] = struct{}{}
	harness.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- harness.SendSSE(SSEEvent{
			Event: "message",
			Data:  `{"type":"token","text":"hello"}`,
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("send sse event: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SendSSE blocked on a backed up client")
	}

	select {
	case payload := <-ready:
		if payload != "event: message\ndata: {\"type\":\"token\",\"text\":\"hello\"}\n\n" {
			t.Fatalf("unexpected payload %q", payload)
		}
	default:
		t.Fatal("ready client did not receive event")
	}
}
