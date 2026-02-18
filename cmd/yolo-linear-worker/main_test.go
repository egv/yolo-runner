package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/linear/webhook"
)

func TestRunMainParsesFlagsAndInvokesRun(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--queue-path", "runner-logs/linear.jobs.jsonl", "--poll-interval", "250ms", "--once"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatal("expected run function to be called")
	}
	if got.queuePath != "runner-logs/linear.jobs.jsonl" {
		t.Fatalf("expected queue path to be parsed, got %q", got.queuePath)
	}
	if got.pollInterval != 250*time.Millisecond {
		t.Fatalf("expected poll interval=250ms, got %s", got.pollInterval)
	}
	if !got.once {
		t.Fatal("expected once mode to be enabled")
	}
}

func TestRunMainRejectsInvalidPollInterval(t *testing.T) {
	called := false
	code := RunMain([]string{"--poll-interval", "0s"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if called {
		t.Fatal("expected run not to be called")
	}
}

func TestRunMainReturnsErrorWhenRunFails(t *testing.T) {
	stderr := captureWorkerStderr(t, func() {
		code := RunMain([]string{}, func(context.Context, runConfig) error {
			return errors.New("process queued job \"job-1\": run linear session job: opencode stall while waiting for output")
		})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})
	if !strings.Contains(stderr, "Category: runtime") {
		t.Fatalf("expected runtime category in stderr, got %q", stderr)
	}
	if strings.Contains(stderr, "Category: webhook") {
		t.Fatalf("expected stderr classification to avoid webhook for wrapped runtime errors, got %q", stderr)
	}
	if !strings.Contains(stderr, "Next step:") {
		t.Fatalf("expected remediation guidance in stderr output, got %q", stderr)
	}
}

func TestDefaultRunConsumesQueuedJobsAndInvokesSessionProcessor(t *testing.T) {
	queuePath := t.TempDir() + "/linear.jobs.jsonl"
	queue := webhook.NewJSONLQueue(queuePath)

	first := webhook.Job{ID: "job-1", IdempotencyKey: "session-1:created:event:1", SessionID: "session-1"}
	second := webhook.Job{ID: "job-2", IdempotencyKey: "session-1:prompted:activity:2", SessionID: "session-1"}
	if err := queue.Enqueue(context.Background(), first); err != nil {
		t.Fatalf("enqueue first job: %v", err)
	}
	if err := queue.Enqueue(context.Background(), second); err != nil {
		t.Fatalf("enqueue second job: %v", err)
	}

	original := processLinearSessionJob
	defer func() {
		processLinearSessionJob = original
	}()

	var mu sync.Mutex
	got := make([]webhook.Job, 0, 2)
	processLinearSessionJob = func(_ context.Context, job webhook.Job) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, job)
		return nil
	}

	err := defaultRun(context.Background(), runConfig{
		queuePath:    queuePath,
		pollInterval: 5 * time.Millisecond,
		once:         true,
	})
	if err != nil {
		t.Fatalf("defaultRun returned error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 processed jobs, got %d", len(got))
	}
	if got[0].ID != first.ID {
		t.Fatalf("expected first job %q, got %q", first.ID, got[0].ID)
	}
	if got[1].ID != second.ID {
		t.Fatalf("expected second job %q, got %q", second.ID, got[1].ID)
	}
}

func TestDefaultRunProcessesQueuedCreatedAndPromptedJobsOutsideWebhookHandler(t *testing.T) {
	queuePath := filepath.Join(t.TempDir(), "linear.jobs.jsonl")
	queue := webhook.NewJSONLQueue(queuePath)
	dispatcher := webhook.NewAsyncDispatcher(queue, 8)

	var activityCalls int32
	var workflowCalls int32
	var sessionUpdateCalls int32
	activityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer lin_api_test" {
			t.Fatalf("expected Authorization header, got %q", got)
		}

		var payload struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL payload: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(payload.Query, "agentActivityCreate"):
			call := atomic.AddInt32(&activityCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"agentActivityCreate": map[string]any{
						"success": true,
						"agentActivity": map[string]any{
							"id": fmt.Sprintf("activity-%d", call),
						},
					},
				},
			})
		case strings.Contains(payload.Query, "agentSessionUpdate"):
			atomic.AddInt32(&sessionUpdateCalls, 1)
			_, _ = w.Write([]byte(`{"data":{"agentSessionUpdate":{"success":true,"agentSession":{"id":"session-1"}}}}`))
		case strings.Contains(payload.Query, "ReadIssueWorkflowForDelegatedRun"):
			atomic.AddInt32(&workflowCalls, 1)
			_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "id": "issue-1",
      "state": {"type": "backlog", "name": "Backlog"},
      "team": {
        "states": {
          "nodes": [
            {"id": "st-started-1", "type": "started", "name": "In Progress"},
            {"id": "st-done", "type": "completed", "name": "Done"}
          ]
        }
      }
    }
  }
}`))
		case strings.Contains(payload.Query, "UpdateIssueWorkflowStateForDelegatedRun"):
			atomic.AddInt32(&workflowCalls, 1)
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
		default:
			t.Fatalf("unexpected GraphQL query: %q", payload.Query)
		}
	}))
	t.Cleanup(activityServer.Close)

	repoRoot := t.TempDir()
	t.Setenv("YOLO_LINEAR_WORKER_BACKEND", "codex")
	t.Setenv("YOLO_LINEAR_WORKER_BINARY", writeFakeCodexBinary(t))
	t.Setenv("YOLO_LINEAR_WORKER_REPO_ROOT", repoRoot)
	t.Setenv("YOLO_LINEAR_WORKER_MODEL", "openai/gpt-5.3-codex")
	t.Setenv("LINEAR_TOKEN", "lin_api_test")
	t.Setenv("LINEAR_API_ENDPOINT", activityServer.URL)

	h := webhook.NewHandler(dispatcher, webhook.HandlerOptions{})
	for _, fixture := range []string{
		"agent_session_event.created.v1.json",
		"agent_session_event.prompted.v1.json",
	} {
		req := httptest.NewRequest(http.MethodPost, "/linear/webhook", bytes.NewReader(readLinearFixture(t, fixture)))
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()

		h.ServeHTTP(rw, req)

		if rw.Code != http.StatusAccepted {
			t.Fatalf("expected webhook ACK status %d, got %d body=%q", http.StatusAccepted, rw.Code, rw.Body.String())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Close(ctx); err != nil {
		t.Fatalf("close dispatcher: %v", err)
	}

	createdLogPath := filepath.Join(repoRoot, "runner-logs", "codex", "evt-created-1.jsonl")
	if _, err := os.Stat(createdLogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no runtime execution during webhook handling, stat err=%v", err)
	}

	if err := defaultRun(context.Background(), runConfig{
		queuePath:    queuePath,
		pollInterval: 5 * time.Millisecond,
		once:         true,
	}); err != nil {
		t.Fatalf("defaultRun returned error: %v", err)
	}

	for _, logPath := range []string{
		createdLogPath,
		filepath.Join(repoRoot, "runner-logs", "codex", "evt-prompted-1.jsonl"),
	} {
		if _, err := os.Stat(logPath); err != nil {
			t.Fatalf("expected runtime log %q, err=%v", logPath, err)
		}
	}

	if got := atomic.LoadInt32(&activityCalls); got != 4 {
		t.Fatalf("expected thought+response activity emission for created+prompted jobs (4 calls), got %d", got)
	}
	if got := atomic.LoadInt32(&sessionUpdateCalls); got != 2 {
		t.Fatalf("expected externalUrls session update for created+prompted jobs (2 calls), got %d", got)
	}
	if got := atomic.LoadInt32(&workflowCalls); got != 2 {
		t.Fatalf("expected delegated issue workflow read+update for created job (2 calls), got %d", got)
	}
}

func writeFakeCodexBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\nprintf 'worker execution complete\\n'\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex binary: %v", err)
	}
	return binaryPath
}

func readLinearFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "linear", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %q: %v", path, err)
	}
	return data
}

func captureWorkerStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return string(data)
}
