package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/sources/arcpr"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceArcPROnceSubmitsAndConsumesOnePRReview(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())
	repoRoot := t.TempDir()
	queuePath := filepath.Join(repoRoot, ".yolo-runner", "queue.db")
	statePath := filepath.Join(repoRoot, "state", "arcpr.db")

	originalConfigService := newSourceArcPRConfigService
	originalLister := sourceArcPRLister
	originalStateFetcher := sourceArcPRStateFetcher
	originalReplyApplier := sourceArcPRReplyApplier
	originalReviewApplier := sourceArcPRReviewApplier
	originalShipGate := sourceArcPRShipGate
	originalAPIClient := newSourceArcPRAPIClient
	t.Cleanup(func() {
		newSourceArcPRConfigService = originalConfigService
		sourceArcPRLister = originalLister
		sourceArcPRStateFetcher = originalStateFetcher
		sourceArcPRReplyApplier = originalReplyApplier
		sourceArcPRReviewApplier = originalReviewApplier
		sourceArcPRShipGate = originalShipGate
		newSourceArcPRAPIClient = originalAPIClient
	})
	newSourceArcPRConfigService = func() arcReviewWatchConfigResolver {
		return staticArcReviewWatchConfigResolver{
			cfg: arcReviewWatchConfig{
				PollInterval: time.Second,
				LockPath:     filepath.Join(repoRoot, "locks", "arcpr.lock"),
				StatePath:    statePath,
				Reviewer:     "alice",
				AllowShip:    true,
			},
		}
	}

	// Stub the reply applier so result consumption stays offline instead of
	// reaching the live Arcanum API.
	sourceArcPRReplyApplier = arcPRReplyApplierFunc(func(_ context.Context, _ arcreview.PRRuntimeState, _ []byte) (arcreview.ReplyResult, error) {
		return arcreview.ReplyResult{}, nil
	})
	sourceArcPRReviewApplier = arcPRReviewApplierFunc(func(_ context.Context, _ arcreview.PRRuntimeState, _ []byte) (arcreview.ReviewResult, error) {
		return arcreview.ReviewResult{}, nil
	})
	sourceArcPRShipGate = arcPRShipGateFunc(func(context.Context, arcreview.ShipGateRequest) (arcreview.ShipGateResult, error) {
		return arcreview.ShipGateResult{}, nil
	})

	var listCalls int
	var fetchedPRs []string
	sourceArcPRLister = arcpr.PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
		listCalls++
		return []arcanum.PRSummary{
			{ID: "777", FromID: "rev-777", Status: "open"},
		}, nil
	})
	sourceArcPRStateFetcher = arcpr.PRStateFetcherFunc(func(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
		fetchedPRs = append(fetchedPRs, workspace+":"+prID)
		return arcreview.PRRuntimeState{
			PRID:     prID,
			Revision: "rev-777",
			Details: arcreview.PRDetails{
				ID:       prID,
				Status:   "open",
				Revision: "rev-777",
			},
			Comments: []arcreview.PRComment{{ID: "c1", Body: "please update this"}},
		}, nil
	})

	runCalled := false
	code := RunMain([]string{"source", "arcpr", "--repo", repoRoot, "--profile", "arc-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source arcpr submit exit code 0, got %d", code)
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for source arcpr")
	}
	if listCalls != 1 {
		t.Fatalf("review PR list calls after submit = %d, want 1", listCalls)
	}
	if len(fetchedPRs) != 0 {
		t.Fatalf("submit poll fetched PR runtime state, want none: %#v", fetchedPRs)
	}

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected queued PR review item")
	}
	if claimed.Kind != workitem.KindPRReview {
		t.Fatalf("claimed kind = %q, want %q", claimed.Kind, workitem.KindPRReview)
	}
	if claimed.Source != "arcpr-arc-dev" {
		t.Fatalf("claimed source = %q, want arcpr-arc-dev", claimed.Source)
	}
	if claimed.SourceRef != "pr:777" {
		t.Fatalf("claimed source ref = %q, want pr:777", claimed.SourceRef)
	}
	payload, err := workitem.DecodePRReviewPayload(claimed.Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if payload.PRID != "777" || payload.Revision != "rev-777" || !payload.Ship || len(payload.UnansweredCommentIDs) != 0 {
		t.Fatalf("payload = %#v, want PR 777 rev-777 no source-side comments ship=true", payload)
	}

	resultPayload, err := json.Marshal(workitem.PRReviewResult{
		Replies:          []workitem.PRReviewReply{{CommentID: "c1", Body: "handled"}},
		ReviewVerdict:    "ok",
		ShipReady:        true,
		RevisionReviewed: "rev-777",
	})
	if err != nil {
		t.Fatalf("marshal result payload: %v", err)
	}
	if err := store.Complete(claimed.ID, workqueue.Result{Payload: resultPayload}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(queue) error = %v", err)
	}

	code = RunMain([]string{"source", "arcpr", "--repo", repoRoot, "--profile", "arc-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called for source arcpr")
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source arcpr consume exit code 0, got %d", code)
	}
	if listCalls != 2 {
		t.Fatalf("review PR list calls after consume = %d, want 2", listCalls)
	}
	if want := []string{":777", ":777"}; !reflect.DeepEqual(fetchedPRs, want) {
		t.Fatalf("fetched PRs = %#v, want %#v", fetchedPRs, want)
	}

	state, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open(state) error = %v", err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Errorf("Close(state) error = %v", err)
		}
	})
	revision, err := state.GetReviewedRevision(ctx, "777")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if revision != "rev-777" {
		t.Fatalf("reviewed revision = %q, want rev-777", revision)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "777")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"c1"}) {
		t.Fatalf("answered comments = %#v, want [c1]", answered)
	}

	store, err = workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue after consume) error = %v", err)
	}
	defer store.Close()
	unconsumed, err := store.ListUnconsumedResults("arcpr-arc-dev")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(unconsumed) != 0 {
		t.Fatalf("unconsumed results = %d, want 0", len(unconsumed))
	}
	next, err := store.Claim("runner-b", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(after consume) error = %v", err)
	}
	if next != nil {
		t.Fatalf("expected no duplicate PR review item after writeback, got %#v", next)
	}
}

func TestSourceArcPROnceUsesSimplifiedConfigIncomingDiscoveryAndAPIWriteback(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ARC_TOKEN", "test-token")
	repoRoot := t.TempDir()
	queuePath := filepath.Join(repoRoot, ".yolo-runner", "queue.db")
	mountsBaseDir := filepath.Join(repoRoot, "configured-pr-mounts")
	statePath := filepath.Join(repoRoot, "state", "arcpr.db")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  arc-dev:
    tracker:
      type: tk
arc_review_watch:
  poll_interval: 1s
  state_path: state/arcpr.db
  reviewer: alice
  allow_ship: false
  mounts_base_dir: `+mountsBaseDir+`
`)

	originalConfigService := newSourceArcPRConfigService
	originalLister := sourceArcPRLister
	originalStateFetcher := sourceArcPRStateFetcher
	originalReplyApplier := sourceArcPRReplyApplier
	originalReviewApplier := sourceArcPRReviewApplier
	originalAPIClient := newSourceArcPRAPIClient
	t.Cleanup(func() {
		newSourceArcPRConfigService = originalConfigService
		sourceArcPRLister = originalLister
		sourceArcPRStateFetcher = originalStateFetcher
		sourceArcPRReplyApplier = originalReplyApplier
		sourceArcPRReviewApplier = originalReviewApplier
		newSourceArcPRAPIClient = originalAPIClient
	})
	newSourceArcPRConfigService = func() arcReviewWatchConfigResolver {
		return newTrackerConfigService()
	}
	sourceArcPRLister = nil
	sourceArcPRStateFetcher = nil

	listCalls := make([]string, 0, 4)
	listCallsMu := &sync.Mutex{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listCallsMu.Lock()
		defer listCallsMu.Unlock()
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("method = %s, want GET", got)
		}
		// Writeback fetches per-PR state over the API (never via an Arc
		// checkout — that would block on the per-PR lock during agent runs).
		if r.URL.Path == "/api/v1/review-requests/777" {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"data":{
  "id":777,"summary":"Ready for review","author":{"name":"bob"},"state":"open",
  "vcs":{"from_branch":"users/alice/pr","to_branch":"trunk"},
  "active_diff_set":{"id":"rev-777"},
  "checks":[{"system":"CI","type":"ci","required":true,"status":"success"}]
}}`)); err != nil {
				t.Fatalf("write PR state response: %v", err)
			}
			return
		}
		if got := r.URL.Path; got != "/api/v1/review-requests" {
			t.Fatalf("path = %q, want /api/v1/review-requests", got)
		}
		query := r.URL.Query()
		switch q := query.Get("query"); {
		case q == "subscriber(alice);open()":
			listCalls = append(listCalls, "reviewer")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`[{"id":"777","from_id":"rev-777","status":"open","summary":"Ready for review","reviewers":["alice"],"to_branch":"trunk"}]`)); err != nil {
				t.Fatalf("write reviewer response: %v", err)
			}
		case q == "author(alice);open()":
			listCalls = append(listCalls, "author")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`[]`)); err != nil {
				t.Fatalf("write author response: %v", err)
			}
		default:
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
	}))
	t.Cleanup(server.Close)

	newSourceArcPRAPIClient = func() (*arcanum.APIClient, error) {
		return arcanum.NewAPIClient(arcanum.APIClientConfig{
			BaseURL:    server.URL + "/api",
			HTTPClient: server.Client(),
			TokenSource: func(context.Context) (string, error) {
				return "test-token", nil
			},
		})
	}

	var replyStates []arcreview.PRRuntimeState
	sourceArcPRReplyApplier = arcPRReplyApplierFunc(func(_ context.Context, state arcreview.PRRuntimeState, _ []byte) (arcreview.ReplyResult, error) {
		replyStates = append(replyStates, state)
		return arcreview.ReplyResult{}, nil
	})
	var reviewStates []arcreview.PRRuntimeState
	sourceArcPRReviewApplier = arcPRReviewApplierFunc(func(_ context.Context, state arcreview.PRRuntimeState, _ []byte) (arcreview.ReviewResult, error) {
		reviewStates = append(reviewStates, state)
		return arcreview.ReviewResult{}, nil
	})

	binDir := t.TempDir()
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	// The source must never shell out to arc: discovery and writeback are both
	// API-backed. Any arc invocation fails the run and therefore the test.
	writeSourceArcPRFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf '%s	arc' "$PWD" >> "$ARC_SOURCE_TEST_CALLS"
for arg in "$@"; do
  printf ' %s' "$arg" >> "$ARC_SOURCE_TEST_CALLS"
done
printf '\n' >> "$ARC_SOURCE_TEST_CALLS"
printf 'source arcpr must not invoke arc (got: %s)\n' "$*" >&2
exit 7
`)
	writeSourceArcPRFakeExecutable(t, binDir, "curl", `#!/bin/sh
set -eu
printf '%s	curl' "$PWD" >> "$ARC_SOURCE_TEST_CALLS"
for arg in "$@"; do
  printf ' %s' "$arg" >> "$ARC_SOURCE_TEST_CALLS"
done
printf '\n' >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/777/comments")
  printf '%s\n' '{"data":[{"id":"c1","content":"please address"}]}'
  ;;
*)
  printf 'unexpected curl args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	t.Setenv("ARC_SOURCE_TEST_CALLS", callsPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	code := RunMain([]string{"source", "arcpr", "--repo", repoRoot, "--profile", "arc-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called for source arcpr")
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source arcpr submit exit code 0, got %d", code)
	}

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected queued PR review item")
	}
	payload, err := workitem.DecodePRReviewPayload(claimed.Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if payload.PRID != "777" || payload.Revision != "rev-777" || payload.Ship {
		t.Fatalf("payload = %#v, want PR 777 rev-777 ship=false", payload)
	}
	resultPayload, err := json.Marshal(workitem.PRReviewResult{
		Replies:          []workitem.PRReviewReply{{CommentID: "c1", Body: "handled"}},
		ReviewVerdict:    "ok",
		ShipReady:        false,
		RevisionReviewed: "rev-777",
	})
	if err != nil {
		t.Fatalf("marshal result payload: %v", err)
	}
	if err := store.Complete(claimed.ID, workqueue.Result{Payload: resultPayload}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(queue) error = %v", err)
	}

	code = RunMain([]string{"source", "arcpr", "--repo", repoRoot, "--profile", "arc-dev", "--queue", queuePath, "--once"}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called for source arcpr")
		return nil
	})
	if code != 0 {
		t.Fatalf("expected source arcpr consume exit code 0, got %d", code)
	}

	if len(replyStates) != 1 {
		t.Fatalf("reply applier calls = %d, want 1", len(replyStates))
	}
	if len(reviewStates) != 1 {
		t.Fatalf("review applier calls = %d, want 1", len(reviewStates))
	}
	if replyStates[0].PRID != "777" || reviewStates[0].Revision != "rev-777" {
		t.Fatalf("writeback states = %#v %#v, want PR 777 revision rev-777", replyStates[0], reviewStates[0])
	}

	rawCalls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("ReadFile(calls) error = %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(rawCalls)), "\n")
	assertSourceArcPRCallsContain(t, calls,
		"curl -fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/777/comments",
	)
	for _, call := range calls {
		if strings.Contains(call, "\tarc ") {
			t.Fatalf("source arcpr invoked arc (%q); discovery and writeback must be API-only", call)
		}
	}

	state, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open(state) error = %v", err)
	}
	defer state.Close()
	revision, err := state.GetReviewedRevision(ctx, "777")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if revision != "rev-777" {
		t.Fatalf("reviewed revision = %q, want rev-777", revision)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "777")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"c1"}) {
		t.Fatalf("answered comments = %#v, want [c1]", answered)
	}
}

func TestBuildSourceArcPRRunBundleConfiguresSourcehostOptions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := t.TempDir()
	queuePath := filepath.Join(repoRoot, ".yolo-runner", "queue.db")
	statePath := filepath.Join(repoRoot, "state", "arcpr.db")

	originalConfigService := newSourceArcPRConfigService
	originalAPIClient := newSourceArcPRAPIClient
	originalLister := sourceArcPRLister
	originalStateFetcher := sourceArcPRStateFetcher
	t.Cleanup(func() {
		newSourceArcPRConfigService = originalConfigService
		newSourceArcPRAPIClient = originalAPIClient
		sourceArcPRLister = originalLister
		sourceArcPRStateFetcher = originalStateFetcher
	})
	newSourceArcPRConfigService = func() arcReviewWatchConfigResolver {
		return staticArcReviewWatchConfigResolver{
			cfg: arcReviewWatchConfig{
				PollInterval:   12 * time.Second,
				LockPath:       filepath.Join(repoRoot, ".yolo-runner", "custom-arcpr.lock"),
				StatePath:      statePath,
				Reviewer:       "alice",
				AllowShip:      true,
				ObjectsBaseDir: filepath.Join(repoRoot, "objects"),
				MountsBaseDir:  filepath.Join(repoRoot, "mounts"),
			},
		}
	}
	newSourceArcPRAPIClient = func() (*arcanum.APIClient, error) {
		return arcanum.NewAPIClient(arcanum.APIClientConfig{
			BaseURL: "https://a.yandex-team.ru/api",
		})
	}
	sourceArcPRLister = arcpr.PRListerFunc(func(context.Context) ([]arcanum.PRSummary, error) {
		return nil, nil
	})
	sourceArcPRStateFetcher = arcpr.PRStateFetcherFunc(func(context.Context, string, string) (arcreview.PRRuntimeState, error) {
		return arcreview.PRRuntimeState{}, nil
	})

	sink := &testSink{}
	bundle, err := buildSourceArcPRRunBundle(context.Background(), sourceArcPRCommandConfig{
		repoRoot:   repoRoot,
		profile:    "arc-dev",
		queuePath:  queuePath,
		once:       true,
		eventsPath: filepath.Join(repoRoot, "arcpr.events.jsonl"),
		eventSink:  sink,
	})
	if err != nil {
		t.Fatalf("buildSourceArcPRRunBundle() error = %v", err)
	}
	defer bundle.Close()

	if !bundle.Options.Once {
		t.Fatalf("sourcehost option once = false, want true")
	}
	if got := bundle.Options.PollInterval; got != 12*time.Second {
		t.Fatalf("sourcehost poll interval = %s, want %s", got, 12*time.Second)
	}
	if got := bundle.Options.LockPath; got != filepath.Join(repoRoot, ".yolo-runner", "custom-arcpr.lock") {
		t.Fatalf("sourcehost lock path = %q, want %q", got, filepath.Join(repoRoot, ".yolo-runner", "custom-arcpr.lock"))
	}
	if got := bundle.Options.EventsPath; got != filepath.Join(repoRoot, "arcpr.events.jsonl") {
		t.Fatalf("sourcehost events path = %q, want %q", got, filepath.Join(repoRoot, "arcpr.events.jsonl"))
	}
	if bundle.Options.EventSink == nil {
		t.Fatalf("sourcehost event sink is nil, want non-nil")
	}
	assertBundleEventSinkWritesToInjectedSinkAndEventsPath(t, sink, bundle.Options.EventSink, bundle.Options.EventsPath)

	source := bundle.Source
	if source == nil {
		t.Fatalf("bundle source is nil")
	}
	if source.SourceName != sourceArcPRSourceName("arc-dev") {
		t.Fatalf("source name = %q, want %q", source.SourceName, sourceArcPRSourceName("arc-dev"))
	}
	if source.Preset != "arc-dev" {
		t.Fatalf("source preset = %q, want arc-dev", source.Preset)
	}
	if source.Reviewer != "alice" {
		t.Fatalf("source reviewer = %q, want alice", source.Reviewer)
	}
	if !source.AllowShip {
		t.Fatalf("source allow ship = false, want true")
	}
	if source.ObjectsBaseDir != filepath.Join(repoRoot, "objects") {
		t.Fatalf("source objects base dir = %q, want %q", source.ObjectsBaseDir, filepath.Join(repoRoot, "objects"))
	}
	if source.MountsBaseDir != filepath.Join(repoRoot, "mounts") {
		t.Fatalf("source mounts base dir = %q, want %q", source.MountsBaseDir, filepath.Join(repoRoot, "mounts"))
	}
	if source.State == nil {
		t.Fatalf("source state is nil")
	}
	if source.Lister == nil {
		t.Fatalf("source lister is nil")
	}
	if source.StateFetcher == nil {
		t.Fatalf("source state fetcher is nil")
	}
	if source.APIClient == nil {
		t.Fatalf("source API client is nil")
	}
}

type arcPRReplyApplierFunc func(context.Context, arcreview.PRRuntimeState, []byte) (arcreview.ReplyResult, error)

func (f arcPRReplyApplierFunc) Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReplyResult, error) {
	return f(ctx, state, payload)
}

type arcPRReviewApplierFunc func(context.Context, arcreview.PRRuntimeState, []byte) (arcreview.ReviewResult, error)

func (f arcPRReviewApplierFunc) Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReviewResult, error) {
	return f(ctx, state, payload)
}

type arcPRShipGateFunc func(context.Context, arcreview.ShipGateRequest) (arcreview.ShipGateResult, error)

func (f arcPRShipGateFunc) GateAndShip(ctx context.Context, request arcreview.ShipGateRequest) (arcreview.ShipGateResult, error) {
	return f(ctx, request)
}

type staticArcReviewWatchConfigResolver struct {
	cfg arcReviewWatchConfig
}

func (r staticArcReviewWatchConfigResolver) ResolveArcReviewWatchConfig(string) (arcReviewWatchConfig, error) {
	return r.cfg, nil
}

func writeSourceArcPRFakeExecutable(t *testing.T, dir string, name string, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake executable %s: %v", name, err)
	}
}

func assertSourceArcPRCallsContain(t *testing.T, calls []string, want ...string) {
	t.Helper()

	for _, expected := range want {
		found := false
		for _, call := range calls {
			if call == expected || strings.HasSuffix(call, "\t"+expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("source arcpr calls missing %q in %#v", expected, calls)
		}
	}
}

func assertStringsEqualInOrder(t *testing.T, got []string, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("command sequence length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command sequence[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
