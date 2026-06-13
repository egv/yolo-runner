package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceStartrekOnceSubmitsPreflightAndConsumesResult(t *testing.T) {
	repoRoot := t.TempDir()
	queuePath := filepath.Join(repoRoot, ".yolo-runner", "queue.db")

	var labelOps []string
	startrek := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v3/issues/_search":
			var body struct {
				Filter map[string]string `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode search request: %v", err)
			}
			if body.Filter["queue"] != "VAY" {
				t.Fatalf("search queue = %q, want VAY", body.Filter["queue"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Total-Count", "1")
			w.Header().Set("X-Total-Pages", "1")
			if body.Filter["tags"] == "yolo-agent-ready" {
				_, _ = w.Write([]byte(`[
					{
						"id": "64200b5f7b5b7c0011223344",
						"key": "VAY-42",
						"summary": "Wire source startrek",
						"description": "Submit this task through the queue source.",
						"tags": ["yolo-agent-ready"],
						"createdBy": {"id": "author-1", "display": "Ada Lovelace"},
						"updatedAt": "2026-05-28T01:02:03.000+0000",
						"status": {"key": "open", "display": "Open"}
					}
				]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "GET /v3/issues/VAY-42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Wire source startrek",
				"description": "Submit this task through the queue source.",
				"tags": ["yolo-agent-ready"],
				"createdBy": {"id": "author-1", "display": "Ada Lovelace"},
				"updatedAt": "2026-05-28T01:02:03.000+0000",
				"status": {"key": "open", "display": "Open"}
			}`))
		case "GET /v3/issues/VAY-42/comments":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{
					"id": 1,
					"text": "Ready to implement.",
					"createdBy": {"id": "author-1", "display": "Ada Lovelace"},
					"createdAt": "2026-05-28T01:03:03.000+0000",
					"updatedAt": "2026-05-28T01:03:03.000+0000"
				}
			]`))
		case "PATCH /v3/issues/VAY-42":
			var body struct {
				Tags map[string][]string `json:"tags"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode label patch: %v", err)
			}
			for _, label := range body.Tags["remove"] {
				labelOps = append(labelOps, "remove "+label)
			}
			for _, label := range body.Tags["add"] {
				labelOps = append(labelOps, "add "+label)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected Startrek request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer startrek.Close()

	t.Setenv("STARTREK_TEST_TOKEN", "token")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  st-dev:
    tracker:
      type: startrek
      startrek:
        endpoint: `+startrek.URL+`/v3
        token_env: STARTREK_TEST_TOKEN
        queues:
          - key: VAY
            root: `+repoRoot+`
tracker_agent:
  poll_interval: 1s
  lock_path: .yolo-runner/source-startrek.lock
`)

	runCalled := false
	code := RunMain([]string{"source", "startrek", "--repo", repoRoot, "--profile", "st-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source startrek submit exit code 0, got %d", code)
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for source startrek")
	}

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected queued Startrek preflight item")
	}
	if claimed.Kind != workitem.KindPreflight {
		t.Fatalf("claimed kind = %q, want %q", claimed.Kind, workitem.KindPreflight)
	}
	if claimed.Source != "startrek-st-dev" {
		t.Fatalf("claimed source = %q, want startrek-st-dev", claimed.Source)
	}
	if claimed.SourceRef != "VAY-42" {
		t.Fatalf("claimed source ref = %q, want VAY-42", claimed.SourceRef)
	}
	if !strings.HasPrefix(claimed.IdempotencyKey, "st/VAY-42/preflight/") {
		t.Fatalf("claimed idempotency key = %q, want st/VAY-42/preflight/<rev>", claimed.IdempotencyKey)
	}
	var payload workitem.PreflightPayload
	if err := json.Unmarshal(claimed.Payload, &payload); err != nil {
		t.Fatalf("decode preflight payload: %v", err)
	}
	if payload.Task.ID != "VAY-42" || payload.Task.Title != "Wire source startrek" {
		t.Fatalf("unexpected preflight task payload: %#v", payload.Task)
	}
	if payload.QueueRoot.ID != "VAY" {
		t.Fatalf("preflight queue root = %q, want VAY", payload.QueueRoot.ID)
	}

	resultPayload, err := json.Marshal(workitem.PreflightResult{
		Verdict:    workitem.PreflightVerdictReady,
		Confidence: 0.91,
		Summary:    "Ready for split planning.",
	})
	if err != nil {
		t.Fatalf("marshal preflight result: %v", err)
	}
	if err := store.Complete(claimed.ID, workqueue.Result{Payload: resultPayload}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(queue) error = %v", err)
	}

	code = RunMain([]string{"source", "startrek", "--repo", repoRoot, "--profile", "st-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called for source startrek")
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source startrek consume exit code 0, got %d", code)
	}
	wantLabels := []string{"remove yolo-agent-in-progress", "add yolo-agent-ready"}
	if !reflect.DeepEqual(labelOps, wantLabels) {
		t.Fatalf("label operations mismatch:\n got: %#v\nwant: %#v", labelOps, wantLabels)
	}

	store, err = workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue after consume) error = %v", err)
	}
	defer store.Close()
	unconsumed, err := store.ListUnconsumedResults("startrek-st-dev")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(unconsumed) != 0 {
		t.Fatalf("unconsumed results = %d, want 0", len(unconsumed))
	}
}
