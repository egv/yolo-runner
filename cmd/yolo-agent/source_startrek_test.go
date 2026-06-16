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

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	startreksource "github.com/egv/yolo-runner/v2/internal/sources/startrek"
	"github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceStartrekQueuesMapsPresets(t *testing.T) {
	got := sourceStartrekQueues([]startrekQueueModel{
		{Key: "VAY", Preset: "queue-a"},
		{Key: "VBO"},
	})
	if len(got) != 2 {
		t.Fatalf("sourceStartrekQueues() length = %d, want 2", len(got))
	}
	if got[0].Key != "VAY" || got[0].Preset != "queue-a" {
		t.Fatalf("first queue = %#v, want key=%q preset=%q", got[0], "VAY", "queue-a")
	}
	if got[1].Key != "VBO" || got[1].Preset != "" {
		t.Fatalf("second queue = %#v, want key=%q preset=%q", got[1], "VBO", "")
	}
}

func TestSourceStartrekOnceSubmitsPreflightAndConsumesResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
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
	wantLabels := []string{
		"remove yolo-agent-in-progress",
		"add yolo-agent-ready",
	}
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

func TestSourceStartrekNeedsInfoHumanReplyResumesExactlyOneFreshPreflight(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := t.TempDir()
	queuePath := filepath.Join(repoRoot, ".yolo-runner", "queue.db")

	startrek := newFakeTrackerWatchStartrek(t)
	defer startrek.Close()

	t.Setenv("STARTREK_TEST_TOKEN", "tracker-token")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  st-dev:
    tracker:
      type: startrek
      startrek:
        endpoint: `+startrek.URL+`
        token_env: STARTREK_TEST_TOKEN
        queues:
          - key: VAY
            root: `+repoRoot+`
tracker_agent:
  poll_interval: 1s
  lock_path: .yolo-runner/source-startrek.lock
`)

	runOnce := func(phase string) {
		t.Helper()
		code := RunMain([]string{"source", "startrek", "--repo", repoRoot, "--profile", "st-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
			t.Fatalf("legacy run function should not be called during %s", phase)
			return nil
		})
		if code != 0 {
			t.Fatalf("source startrek %s exit code = %d, want 0", phase, code)
		}
	}

	runOnce("initial discovery")

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	first, err := store.Claim("runner-a", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first preflight) error = %v", err)
	}
	if first == nil {
		t.Fatalf("expected initial Startrek preflight item")
	}
	if first.Kind != workitem.KindPreflight || first.SourceRef != "VAY-42" {
		t.Fatalf("unexpected initial preflight item: %#v", first)
	}
	firstKey := first.IdempotencyKey

	needsInfoPayload, err := json.Marshal(workitem.PreflightResult{
		Verdict:   workitem.PreflightVerdictNeedsInfo,
		Summary:   "Ownership is unclear.",
		Questions: []string{"Which package owns this behavior?"},
	})
	if err != nil {
		t.Fatalf("marshal needs-info preflight result: %v", err)
	}
	if err := store.Complete(first.ID, workqueue.Result{Payload: needsInfoPayload}); err != nil {
		t.Fatalf("Complete(needs-info preflight) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(queue after needs-info result) error = %v", err)
	}

	runOnce("needs-info writeback")

	labels := startrek.labels("VAY-42")
	if hasLabel(labels, "yolo-agent-ready") {
		t.Fatalf("expected needs-info writeback to remove ready label, got %#v", labels)
	}
	if hasLabel(labels, "yolo-agent-in-progress") {
		t.Fatalf("expected needs-info writeback to remove in-progress label, got %#v", labels)
	}
	if !hasLabel(labels, "needs-info") {
		t.Fatalf("expected needs-info writeback to add needs-info label, got %#v", labels)
	}
	if got := countNeedsInfoComments(startrek.commentTexts()); got != 1 {
		t.Fatalf("needs-info marker comments = %d, want 1", got)
	}

	startrek.addComment("author-1", "The package owner is adapta/messenger; use secret sec-123.")
	startrek.mu.Lock()
	startrek.issue["updatedAt"] = "2026-05-28T05:02:00.000+0000"
	startrek.mu.Unlock()

	runOnce("human reply resume")

	store, err = workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue after resume) error = %v", err)
	}
	defer store.Close()
	second, err := store.Claim("runner-b", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(resumed preflight) error = %v", err)
	}
	if second == nil {
		t.Fatalf("expected one resumed Startrek preflight item after human reply")
	}
	if second.Kind != workitem.KindPreflight || second.SourceRef != "VAY-42" {
		t.Fatalf("unexpected resumed preflight item: %#v", second)
	}
	if second.IdempotencyKey == firstKey {
		t.Fatalf("resumed preflight idempotency key = %q, want fresh key distinct from first preflight", second.IdempotencyKey)
	}
	extra, err := store.Claim("runner-c", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(extra preflight) error = %v", err)
	}
	if extra != nil {
		t.Fatalf("expected exactly one resumed preflight item, got extra item %#v", extra)
	}
}

func TestSourceStartrekPollSkipsReadyIssueWithOpenQueueItemAfterRevisionChange(t *testing.T) {
	ctx := context.Background()
	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	defer store.Close()

	backend := &fakeSourceStartrekPollBackend{revision: "rev-1"}
	source := &sourceStartrekRuntimeSource{
		Source: &startreksource.Source{
			SourceName: "startrek-st-dev",
			Queue:      store,
		},
		Backend: backend,
		Queues:  []startrekQueueModel{{Key: "VAY"}},
		Preset:  "st-dev",
	}

	first, err := source.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(first) error = %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("Poll(first) submissions = %d, want 1", len(first))
	}
	if _, err := store.Enqueue(first[0]); err != nil {
		t.Fatalf("Enqueue(first preflight) error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first preflight) error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected first queued Startrek preflight item")
	}

	backend.revision = "rev-2"
	second, err := source.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(after revision change) error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("Poll(after revision change) submissions = %d, want 0 while item %s is open; first=%s second=%s", len(second), claimed.ID, first[0].IdempotencyKey, second[0].IdempotencyKey)
	}
}

func TestSourceStartrekPollDoesNotClaimIssueWhenEnqueueFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}

	backend := &fakeSourceStartrekPollBackend{
		revision: "rev-1",
		labels:   []string{"yolo-agent-ready"},
	}
	source := &sourceStartrekRuntimeSource{
		Source: &startreksource.Source{
			SourceName:      "startrek-st-dev",
			Queue:           store,
			ReadyLabel:      "yolo-agent-ready",
			ProcessingLabel: "yolo-agent-in-progress",
		},
		Backend: backend,
		Queues:  []startrekQueueModel{{Key: "VAY"}},
		Preset:  "st-dev",
	}

	err = startrekSourcehostRunWithQueueCloseAfterPoll(ctx, source, store)
	if err == nil {
		t.Fatalf("expected sourcehost run to fail when queue closes before enqueue")
	}
	if got := backend.labelOps; len(got) != 0 {
		t.Fatalf("Poll claimed Startrek issue before durable enqueue failure; label ops = %#v", got)
	}
	if !hasLabel(backend.labels, "yolo-agent-ready") || hasLabel(backend.labels, "yolo-agent-in-progress") {
		t.Fatalf("failed enqueue should leave issue ready and unclaimed, labels = %#v", backend.labels)
	}

	reopened, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen queue after failed enqueue: %v", err)
	}
	defer reopened.Close()
	claimed, err := reopened.Claim("runner-a", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(after failed enqueue) error = %v", err)
	}
	if claimed != nil {
		t.Fatalf("expected no queued item after failed enqueue, got %#v", claimed)
	}
}

func startrekSourcehostRunWithQueueCloseAfterPoll(ctx context.Context, source *sourceStartrekRuntimeSource, store *workqueue.Store) error {
	wrapped := &closeStartrekQueueAfterPollSource{source: source, store: store}
	return sourcehost.Run(ctx, wrapped, store, sourcehost.Options{Once: true})
}

type closeStartrekQueueAfterPollSource struct {
	source *sourceStartrekRuntimeSource
	store  *workqueue.Store
}

func (s *closeStartrekQueueAfterPollSource) Name() string {
	return s.source.Name()
}

func (s *closeStartrekQueueAfterPollSource) Poll(ctx context.Context) ([]workqueue.Submission, error) {
	submissions, err := s.source.Poll(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.store.Close(); err != nil {
		return nil, err
	}
	return submissions, nil
}

func (s *closeStartrekQueueAfterPollSource) HandleResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	return s.source.HandleResult(ctx, item, result)
}

type fakeSourceStartrekPollBackend struct {
	revision string
	labelOps []string
	labels   []string
}

func (b *fakeSourceStartrekPollBackend) ResumeNeedsInfoTasks(context.Context, startrek.NeedsInfoResumeInput) ([]string, error) {
	return nil, nil
}

func (b *fakeSourceStartrekPollBackend) GetTaskTree(context.Context, string) (*contracts.TaskTree, error) {
	root := contracts.Task{ID: "VAY", Title: "VAY", Status: contracts.TaskStatusOpen}
	task := contracts.Task{
		ID:       "VAY-42",
		Title:    "Wire source startrek",
		Status:   contracts.TaskStatusOpen,
		ParentID: root.ID,
		Metadata: map[string]string{"revision": b.revision},
	}
	return &contracts.TaskTree{
		Root: root,
		Tasks: map[string]contracts.Task{
			root.ID: root,
			task.ID: task,
		},
		Relations: []contracts.TaskRelation{{
			FromID: root.ID,
			ToID:   task.ID,
			Type:   contracts.RelationParent,
		}},
	}, nil
}

func (b *fakeSourceStartrekPollBackend) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	return &contracts.Task{ID: taskID, Title: taskID, Status: contracts.TaskStatusOpen}, nil
}

func (b *fakeSourceStartrekPollBackend) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	return nil
}

func (b *fakeSourceStartrekPollBackend) SetTaskData(context.Context, string, map[string]string) error {
	return nil
}

func (b *fakeSourceStartrekPollBackend) RemoveLabel(_ context.Context, _ string, label string) error {
	b.labelOps = append(b.labelOps, "remove")
	b.labels = removeLabel(b.labels, label)
	return nil
}

func (b *fakeSourceStartrekPollBackend) AddLabel(_ context.Context, _ string, label string) error {
	b.labelOps = append(b.labelOps, "add")
	if !hasLabel(b.labels, label) {
		b.labels = append(b.labels, strings.TrimSpace(label))
	}
	return nil
}

func (b *fakeSourceStartrekPollBackend) CreateIssue(context.Context, startrek.IssueCreateOptions) (startrek.Issue, error) {
	return startrek.Issue{}, nil
}

func (b *fakeSourceStartrekPollBackend) GetIssueComments(context.Context, string) ([]startrek.IssueComment, error) {
	return nil, nil
}

func (b *fakeSourceStartrekPollBackend) CreateIssueComment(context.Context, string, startrek.IssueCommentCreateOptions) (startrek.IssueComment, error) {
	return startrek.IssueComment{}, nil
}
