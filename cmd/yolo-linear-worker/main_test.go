package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	code := RunMain([]string{}, func(context.Context, runConfig) error {
		return errors.New("boom")
	})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
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
	var externalURLCalls int32
	type externalURL struct {
		Label string
		URL   string
	}
	type sessionUpdateCall struct {
		SessionID    string
		ExternalURLs []externalURL
	}
	var sessionUpdatesMu sync.Mutex
	sessionUpdates := make([]sessionUpdateCall, 0, 2)
	activityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer lin_api_test" {
			t.Fatalf("expected Authorization header, got %q", got)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode activity mutation payload: %v", err)
		}

		query, _ := payload["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(query, "agentActivityCreate"):
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
		case strings.Contains(query, "agentSessionUpdate"):
			atomic.AddInt32(&externalURLCalls, 1)
			variables, ok := payload["variables"].(map[string]any)
			if !ok {
				t.Fatalf("expected variables object for session update, got %T", payload["variables"])
			}
			input, ok := variables["input"].(map[string]any)
			if !ok {
				t.Fatalf("expected variables.input object for session update, got %T", variables["input"])
			}
			sessionID, _ := input["id"].(string)
			rawExternalURLs, ok := input["externalUrls"].([]any)
			if !ok {
				t.Fatalf("expected externalUrls array in session update, got %T", input["externalUrls"])
			}
			urls := make([]externalURL, 0, len(rawExternalURLs))
			for i, raw := range rawExternalURLs {
				entry, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("expected externalUrls[%d] to be object, got %T", i, raw)
				}
				label, _ := entry["label"].(string)
				value, _ := entry["url"].(string)
				urls = append(urls, externalURL{Label: label, URL: value})
			}
			sessionUpdatesMu.Lock()
			sessionUpdates = append(sessionUpdates, sessionUpdateCall{
				SessionID:    sessionID,
				ExternalURLs: urls,
			})
			sessionUpdatesMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"agentSessionUpdate": map[string]any{
						"success": true,
					},
				},
			})
		default:
			t.Fatalf("unexpected graphql mutation query: %q", query)
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
	if got := atomic.LoadInt32(&externalURLCalls); got != 2 {
		t.Fatalf("expected externalUrls updates for created+prompted jobs (2 calls), got %d", got)
	}

	sessionURL := mustFileURL(t, filepath.Join(repoRoot, "runner-logs", "codex"))
	expectedLogURLs := map[string]struct{}{
		mustFileURL(t, createdLogPath): {},
		mustFileURL(t, filepath.Join(repoRoot, "runner-logs", "codex", "evt-prompted-1.jsonl")): {},
	}
	sessionUpdatesMu.Lock()
	defer sessionUpdatesMu.Unlock()
	if len(sessionUpdates) != 2 {
		t.Fatalf("expected 2 session update payloads, got %d", len(sessionUpdates))
	}
	for i, update := range sessionUpdates {
		if update.SessionID != "session-1" {
			t.Fatalf("expected session update[%d] to target session-1, got %q", i, update.SessionID)
		}
		if len(update.ExternalURLs) < 2 {
			t.Fatalf("expected at least 2 external urls in session update[%d], got %d", i, len(update.ExternalURLs))
		}
		seen := map[string]struct{}{}
		sawSessionURL := false
		sawLogURL := false
		for _, entry := range update.ExternalURLs {
			if strings.TrimSpace(entry.Label) == "" || strings.TrimSpace(entry.URL) == "" {
				t.Fatalf("session update[%d] contains empty external url entry: %#v", i, entry)
			}
			key := entry.Label + "\n" + entry.URL
			if _, exists := seen[key]; exists {
				t.Fatalf("session update[%d] external urls should be unique, duplicate entry=%#v", i, entry)
			}
			seen[key] = struct{}{}
			if _, err := url.Parse(entry.URL); err != nil {
				t.Fatalf("session update[%d] contains invalid url %q: %v", i, entry.URL, err)
			}
			if entry.Label == "Runner Session" && entry.URL == sessionURL {
				sawSessionURL = true
			}
			if entry.Label == "Runner Log" {
				if _, ok := expectedLogURLs[entry.URL]; ok {
					sawLogURL = true
				}
			}
		}
		if !sawSessionURL {
			t.Fatalf("session update[%d] missing runner session url %q: %#v", i, sessionURL, update.ExternalURLs)
		}
		if !sawLogURL {
			t.Fatalf("session update[%d] missing expected runner log url: %#v", i, update.ExternalURLs)
		}
	}
}

func mustFileURL(t *testing.T, path string) string {
	t.Helper()
	u := &url.URL{Scheme: "file", Path: filepath.Clean(path)}
	return u.String()
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
