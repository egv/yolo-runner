package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestTrackerWatchStartrekNeedsInfoPostsQuestionsAndDoesNotReselectBeforeAuthorReply(t *testing.T) {
	repoRoot := t.TempDir()
	startrek := newFakeTrackerWatchStartrek(t)
	defer startrek.Close()

	callsPath := filepath.Join(repoRoot, "fake-codex-calls")
	fakeCodexPath := writeTrackerWatchFakeCodex(t, repoRoot)
	writeTrackerWatchFakeCodexBackend(t, repoRoot, fakeCodexPath)
	writeTrackerConfigYAML(t, repoRoot, fmt.Sprintf(`
agent:
  backend: codex-cli
  model: fake-codex
  runner_timeout: 5s
default_profile: startrek-demo
profiles:
  startrek-demo:
    tracker:
      type: startrek
      startrek:
        endpoint: %s
        token_env: STARTREK_TOKEN
        queues:
          - key: VAY
            root: %s
tracker_agent:
  labels:
    ready: yolo-agent-ready
    in_progress: yolo-agent-in-progress
    blocked: yolo-agent-blocked
`, strconv.Quote(startrek.URL), strconv.Quote(repoRoot)))

	t.Setenv("STARTREK_TOKEN", "tracker-token")
	t.Setenv("FAKE_CODEX_CALLS", callsPath)

	err := defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot: repoRoot,
		profile:  "startrek-demo",
		once:     true,
	})
	if err != nil {
		t.Fatalf("first tracker-watch iteration failed: %v", err)
	}

	if got := fakeCodexCallCount(t, callsPath); got != 1 {
		t.Fatalf("expected one fake Codex preflight call, got %d", got)
	}
	comments := startrek.commentTexts()
	if len(comments) != 1 {
		t.Fatalf("expected one needs-info comment, got %d", len(comments))
	}
	for _, want := range []string{
		"<!-- yolo-runner:needs-info -->",
		"Ownership is unclear.",
		"Which package owns this behavior?",
		"Who should answer follow-up questions?",
	} {
		if !strings.Contains(comments[0], want) {
			t.Fatalf("expected needs-info comment to contain %q, got:\n%s", want, comments[0])
		}
	}

	labels := startrek.labels("VAY-42")
	if hasLabel(labels, "yolo-agent-ready") {
		t.Fatalf("expected ready label to be removed after needs-info, got %#v", labels)
	}
	if hasLabel(labels, "yolo-agent-in-progress") {
		t.Fatalf("expected in-progress label to be removed after needs-info, got %#v", labels)
	}
	if !hasLabel(labels, "needs-info") {
		t.Fatalf("expected needs-info label after preflight questions, got %#v", labels)
	}

	err = defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot: repoRoot,
		profile:  "startrek-demo",
		once:     true,
	})
	if err != nil {
		t.Fatalf("second tracker-watch iteration failed: %v", err)
	}

	if got := fakeCodexCallCount(t, callsPath); got != 1 {
		t.Fatalf("expected task not to be reselected before author reply; fake Codex calls=%d", got)
	}
	if comments := startrek.commentTexts(); len(comments) != 1 {
		t.Fatalf("expected no duplicate needs-info comments before author reply, got %d", len(comments))
	}
}

type fakeTrackerWatchStartrek struct {
	*httptest.Server

	t        *testing.T
	mu       sync.Mutex
	issue    map[string]any
	comments []map[string]any
}

func newFakeTrackerWatchStartrek(t *testing.T) *fakeTrackerWatchStartrek {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "startrek", "testdata", "tracker_watch_ready_issue.json"))
	if err != nil {
		t.Fatalf("read Startrek fixture: %v", err)
	}
	var issue map[string]any
	if err := json.Unmarshal(raw, &issue); err != nil {
		t.Fatalf("decode Startrek fixture: %v", err)
	}

	fake := &fakeTrackerWatchStartrek{
		t:     t,
		issue: issue,
	}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.handle))
	return fake
}

func (f *fakeTrackerWatchStartrek) handle(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "OAuth tracker-token" {
		f.t.Fatalf("expected Startrek OAuth token, got %q", got)
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/issues/_search":
		f.handleSearch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/issues/VAY-42":
		f.writeJSON(w, http.StatusOK, f.issueSnapshot())
	case r.Method == http.MethodGet && r.URL.Path == "/issues/VAY-42/comments":
		f.writeJSON(w, http.StatusOK, f.commentsSnapshot())
	case r.Method == http.MethodPatch && r.URL.Path == "/issues/VAY-42":
		f.handleLabelPatch(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/issues/VAY-42/comments":
		f.handleCreateComment(w, r)
	default:
		f.t.Fatalf("unexpected Startrek request: %s %s", r.Method, r.URL.String())
	}
}

func (f *fakeTrackerWatchStartrek) handleSearch(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Filter map[string]any `json:"filter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode Startrek search: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprint(payload.Filter["queue"])); got != "VAY" {
		f.t.Fatalf("expected VAY queue search, got %q", got)
	}
	label := strings.TrimSpace(fmt.Sprint(payload.Filter["tags"]))

	f.mu.Lock()
	include := hasLabel(mapStringSlice(f.issue["tags"]), label)
	issue := cloneJSONMap(f.issue)
	f.mu.Unlock()

	w.Header().Set("X-Total-Pages", "1")
	if include {
		w.Header().Set("X-Total-Count", "1")
		f.writeJSON(w, http.StatusOK, []map[string]any{issue})
		return
	}
	w.Header().Set("X-Total-Count", "0")
	f.writeJSON(w, http.StatusOK, []map[string]any{})
}

func (f *fakeTrackerWatchStartrek) handleLabelPatch(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Tags map[string][]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode Startrek label patch: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	labels := mapStringSlice(f.issue["tags"])
	for _, label := range payload.Tags["remove"] {
		labels = removeLabel(labels, label)
	}
	for _, label := range payload.Tags["add"] {
		if !hasLabel(labels, label) {
			labels = append(labels, strings.TrimSpace(label))
		}
	}
	f.issue["tags"] = labels
	f.writeJSON(w, http.StatusOK, map[string]any{})
}

func (f *fakeTrackerWatchStartrek) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Text       string   `json:"text"`
		Summonees  []string `json:"summonees"`
		MarkupType string   `json:"markupType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode Startrek create comment: %v", err)
	}
	comment := map[string]any{
		"id":   len(f.comments) + 1,
		"text": payload.Text,
		"createdBy": map[string]any{
			"id":      "runner",
			"display": "YOLO Runner",
		},
		"createdAt": "2026-05-28T05:00:00.000+0000",
		"updatedAt": "2026-05-28T05:00:00.000+0000",
	}

	f.mu.Lock()
	f.comments = append(f.comments, comment)
	f.mu.Unlock()
	f.writeJSON(w, http.StatusCreated, comment)
}

func (f *fakeTrackerWatchStartrek) issueSnapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneJSONMap(f.issue)
}

func (f *fakeTrackerWatchStartrek) commentsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.comments))
	for _, comment := range f.comments {
		out = append(out, cloneJSONMap(comment))
	}
	return out
}

func (f *fakeTrackerWatchStartrek) labels(issueID string) []string {
	if issueID != "VAY-42" {
		f.t.Fatalf("unexpected issue ID %q", issueID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), mapStringSlice(f.issue["tags"])...)
}

func (f *fakeTrackerWatchStartrek) commentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.comments))
	for _, comment := range f.comments {
		out = append(out, strings.TrimSpace(fmt.Sprint(comment["text"])))
	}
	return out
}

func (f *fakeTrackerWatchStartrek) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		f.t.Fatalf("encode Startrek response: %v", err)
	}
}

func writeTrackerWatchFakeCodex(t *testing.T, repoRoot string) string {
	t.Helper()
	path := filepath.Join(repoRoot, "fake-codex")
	script := strings.Join([]string{
		"#!/bin/sh",
		`printf . >> "$FAKE_CODEX_CALLS"`,
		`printf '%s\n' '{"decision":"needs_info","confidence":0.42,"summary":"Ownership is unclear.","questions":["Which package owns this behavior?","Who should answer follow-up questions?"]}'`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func writeTrackerWatchFakeCodexBackend(t *testing.T, repoRoot string, binary string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir coding agents dir: %v", err)
	}
	payload := fmt.Sprintf(`
name: codex-cli
type: command
backend: codex-cli
model: fake-codex
binary: %s
args:
  - "{{prompt}}"
adapter: command
supports_review: true
supports_stream: true
`, strconv.Quote(binary))
	if err := os.WriteFile(filepath.Join(dir, "codex-cli.yaml"), []byte(strings.TrimSpace(payload)+"\n"), 0o644); err != nil {
		t.Fatalf("write fake codex backend: %v", err)
	}
}

func fakeCodexCallCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read fake codex calls: %v", err)
	}
	return strings.Count(string(raw), ".")
}

func cloneJSONMap(in map[string]any) map[string]any {
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

func mapStringSlice(raw any) []string {
	values, ok := raw.([]any)
	if ok {
		out := make([]string, 0, len(values))
		for _, value := range values {
			if label := strings.TrimSpace(fmt.Sprint(value)); label != "" {
				out = append(out, label)
			}
		}
		return out
	}
	labels, ok := raw.([]string)
	if !ok {
		return nil
	}
	return append([]string(nil), labels...)
}

func hasLabel(labels []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, label := range labels {
		if strings.TrimSpace(label) == want {
			return true
		}
	}
	return false
}

func removeLabel(labels []string, remove string) []string {
	remove = strings.TrimSpace(remove)
	out := labels[:0]
	for _, label := range labels {
		if strings.TrimSpace(label) != remove {
			out = append(out, label)
		}
	}
	return out
}
