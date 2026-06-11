package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestDefaultDiscoverArcReviewPRsAggregatesConfiguredWorkspacesAndDedupesByPRID(t *testing.T) {
	originalList := listArcReviewWorkspacePRs
	t.Cleanup(func() {
		listArcReviewWorkspacePRs = originalList
	})

	calls := []string{}
	listArcReviewWorkspacePRs = func(ctx context.Context, workspace string) ([]arcanum.PRSummary, error) {
		if ctx == nil {
			t.Fatal("listArcReviewWorkspacePRs() context is nil")
		}
		calls = append(calls, workspace)
		switch workspace {
		case "/arcadia/users/alice/review-1":
			return []arcanum.PRSummary{
				{
					ID:        "ARCADIA-501",
					Reviewers: []string{"alice"},
					Branch:    "trunk",
					Status:    "open",
				},
				{
					ID:        "ARCADIA-502",
					Reviewers: []string{"alice"},
					Branch:    "release",
					Status:    "open",
				},
				{
					ID:        "ARCADIA-503",
					Reviewers: []string{"bob"},
					Branch:    "trunk",
					Status:    "open",
				},
			}, nil
		case "/arcadia/users/alice/review-2":
			return []arcanum.PRSummary{
				{
					ID:        "ARCADIA-501",
					Reviewers: []string{"alice"},
					Branch:    "trunk",
					Status:    "open",
				},
				{
					ID:        "ARCADIA-504",
					Reviewers: []string{"alice"},
					Branch:    "trunk",
					Status:    "open",
				},
			}, nil
		default:
			t.Fatalf("unexpected workspace %q", workspace)
			return nil, nil
		}
	}

	got, err := defaultDiscoverArcReviewPRs(arcReviewWatchCommandConfig{}, arcReviewWatchConfig{
		Reviewer:   "alice",
		Workspaces: []string{"/arcadia/users/alice/review-1", "/arcadia/users/alice/review-2"},
		Branches:   []string{"trunk"},
	})
	if err != nil {
		t.Fatalf("defaultDiscoverArcReviewPRs() error = %v", err)
	}

	wantCalls := []string{"/arcadia/users/alice/review-1", "/arcadia/users/alice/review-2"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("listArcReviewWorkspacePRs() calls = %#v, want %#v", calls, wantCalls)
	}
	want := []arcReviewDiscoveredPR{
		{
			ID:        "ARCADIA-501",
			Workspace: "/arcadia/users/alice/review-1",
			Branch:    "trunk",
		},
		{
			ID:        "ARCADIA-504",
			Workspace: "/arcadia/users/alice/review-2",
			Branch:    "trunk",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultDiscoverArcReviewPRs() = %#v, want %#v", got, want)
	}
}

func TestRunArcReviewWatchPollIterationReconcilesDiscoveredPRsOnce(t *testing.T) {
	repoRoot := t.TempDir()
	statePath := filepath.Join(repoRoot, "state", "arc-review-watch.db")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  state_path: state/arc-review-watch.db
  branches:
    - trunk
`)

	originalDiscover := discoverArcReviewPRs
	originalLiveness := defaultArcReviewPIDLiveness
	t.Cleanup(func() {
		discoverArcReviewPRs = originalDiscover
		defaultArcReviewPIDLiveness = originalLiveness
	})
	defaultArcReviewPIDLiveness = arcReviewPIDLivenessFunc(func(int) bool {
		return true
	})
	discoverCalls := 0
	discoverArcReviewPRs = func(_ arcReviewWatchCommandConfig, cfg arcReviewWatchConfig) ([]arcReviewDiscoveredPR, error) {
		discoverCalls++
		if cfg.StatePath != statePath {
			t.Fatalf("discoverArcReviewPRs() state path = %q, want %q", cfg.StatePath, statePath)
		}
		if got := strings.Join(cfg.Branches, ","); got != "trunk" {
			t.Fatalf("discoverArcReviewPRs() branches = %q, want trunk", got)
		}
		return []arcReviewDiscoveredPR{
			{
				ID:        "ARCADIA-201",
				Workspace: "/arcadia/users/alice/pr-201",
				Branch:    "trunk",
				Revision:  "rev-201",
			},
			{
				ID:        "ARCADIA-201",
				Workspace: "/arcadia/users/alice/pr-201-duplicate",
				Branch:    "trunk",
				Revision:  "rev-201-duplicate",
			},
			{
				ID:        "ARCADIA-202",
				Workspace: "/arcadia/users/alice/pr-202-new",
				Branch:    "trunk",
				Revision:  "rev-202-new",
			},
		}, nil
	}

	store, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	existing := arcreviewstate.Session{
		ID:           "session-existing",
		PRID:         "ARCADIA-202",
		Workspace:    "/arcadia/users/alice/pr-202",
		Branch:       "trunk",
		Status:       "running",
		PID:          4242,
		Revision:     "rev-202",
		Heartbeat:    time.Now().UTC(),
		FailureCount: 1,
		LogPath:      "/tmp/pr-202.log",
	}
	if _, err := store.CreateSession(existing); err != nil {
		t.Fatalf("CreateSession(existing) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	cfg := arcReviewWatchCommandConfig{repoRoot: repoRoot}
	if err := runArcReviewWatchPollIteration(cfg); err != nil {
		t.Fatalf("first runArcReviewWatchPollIteration() error = %v", err)
	}
	if err := runArcReviewWatchPollIteration(cfg); err != nil {
		t.Fatalf("second runArcReviewWatchPollIteration() error = %v", err)
	}
	if discoverCalls != 2 {
		t.Fatalf("discoverArcReviewPRs() calls = %d, want 2", discoverCalls)
	}

	reopened, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("reopen state error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	sessions, err := reopened.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() returned %d sessions, want 2: %#v", len(sessions), sessions)
	}
	created, err := reopened.ListSessionsByPRID("ARCADIA-201")
	if err != nil {
		t.Fatalf("ListSessionsByPRID(new) error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("new PR sessions = %d, want 1: %#v", len(created), created)
	}
	if got := created[0]; got.Status != "pending" ||
		got.Workspace != "/arcadia/users/alice/pr-201" ||
		got.Branch != "trunk" ||
		got.Revision != "rev-201" {
		t.Fatalf("created session mismatch: %#v", got)
	}
	unchanged, err := reopened.GetSession("session-existing")
	if err != nil {
		t.Fatalf("GetSession(existing) error = %v", err)
	}
	if unchanged != existing {
		t.Fatalf("existing session changed\ngot:  %#v\nwant: %#v", unchanged, existing)
	}
}

func TestDefaultRunArcReviewWatchEmitsWarningAndContinuesAfterIterationError(t *testing.T) {
	repoRoot := t.TempDir()
	eventsPath := filepath.Join(repoRoot, "runner-logs", "arc-review-watch.events.jsonl")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  poll_interval: 1ms
`)

	originalDiscover := discoverArcReviewPRs
	t.Cleanup(func() {
		discoverArcReviewPRs = originalDiscover
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	transientErr := errors.New("temporary arcanum outage")
	discoverCalls := 0
	discoverArcReviewPRs = func(_ arcReviewWatchCommandConfig, _ arcReviewWatchConfig) ([]arcReviewDiscoveredPR, error) {
		discoverCalls++
		if discoverCalls == 1 {
			return nil, transientErr
		}
		cancel()
		return nil, nil
	}

	err := defaultRunArcReviewWatch(ctx, arcReviewWatchCommandConfig{
		repoRoot:   repoRoot,
		profile:    "default",
		dryRun:     true,
		eventsPath: eventsPath,
	})
	if err != nil {
		t.Fatalf("expected arc-review-watch to keep running after transient iteration error, got %v", err)
	}
	if discoverCalls < 2 {
		t.Fatalf("expected polling to continue after transient error, got %d discover calls", discoverCalls)
	}

	events := readTrackerWatchEvents(t, eventsPath)
	for _, event := range events {
		if event.Type != contracts.EventTypeRunnerWarning {
			continue
		}
		if !strings.Contains(event.Message, "temporary arcanum outage") {
			t.Fatalf("expected warning to include iteration error cause, got %q", event.Message)
		}
		return
	}
	t.Fatalf("expected runner_warning event, got %#v", events)
}

func TestReconcileArcReviewSessionsCreatesMissingPendingSessionsAndKeepsExistingNonTerminal(t *testing.T) {
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	existing := arcreviewstate.Session{
		ID:           "session-existing",
		PRID:         "ARCADIA-102",
		Workspace:    "/repo/workspaces/pr-102",
		Branch:       "arc-review/102",
		Status:       "running",
		PID:          4242,
		Revision:     "r-existing",
		FailureCount: 2,
		LogPath:      "/tmp/pr-102.log",
	}
	if _, err := store.CreateSession(existing); err != nil {
		t.Fatalf("CreateSession(existing) error = %v", err)
	}

	created, err := reconcileArcReviewSessions(store, []arcReviewDiscoveredPR{
		{
			ID:        "ARCADIA-101",
			Workspace: "/repo/workspaces/pr-101",
			Branch:    "arc-review/101",
			Revision:  "r-new",
		},
		{
			ID:        "ARCADIA-102",
			Workspace: "/repo/workspaces/pr-102-new",
			Branch:    "arc-review/102-new",
			Revision:  "r-newer",
		},
	})
	if err != nil {
		t.Fatalf("reconcileArcReviewSessions() error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("reconcileArcReviewSessions() created %d sessions, want 1: %#v", len(created), created)
	}
	if got := created[0]; got.PRID != "ARCADIA-101" ||
		got.Workspace != "/repo/workspaces/pr-101" ||
		got.Branch != "arc-review/101" ||
		got.Revision != "r-new" ||
		got.Status != "pending" {
		t.Fatalf("created session mismatch: %#v", got)
	}

	createdAgain, err := reconcileArcReviewSessions(store, []arcReviewDiscoveredPR{
		{
			ID:        "ARCADIA-101",
			Workspace: "/repo/workspaces/pr-101",
			Branch:    "arc-review/101",
			Revision:  "r-new",
		},
	})
	if err != nil {
		t.Fatalf("second reconcileArcReviewSessions() error = %v", err)
	}
	if len(createdAgain) != 0 {
		t.Fatalf("second reconcileArcReviewSessions() created %d sessions, want 0: %#v", len(createdAgain), createdAgain)
	}

	unchanged, err := store.GetSession("session-existing")
	if err != nil {
		t.Fatalf("GetSession(existing) error = %v", err)
	}
	if unchanged != existing {
		t.Fatalf("existing session changed\ngot:  %#v\nwant: %#v", unchanged, existing)
	}
}

func TestRestartStaleArcReviewSessionsMarksCrashedAndStartsReplacement(t *testing.T) {
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, 6, 9, 15, 0, 0, 0, time.UTC)
	fresh := arcreviewstate.Session{
		ID:           "session-fresh",
		PRID:         "ARCADIA-301",
		Workspace:    "/repo/workspaces/pr-301",
		Branch:       "trunk",
		Status:       "running",
		PID:          301,
		Revision:     "r-fresh",
		Heartbeat:    now.Add(-1 * time.Minute),
		FailureCount: 1,
		LogPath:      "/tmp/pr-301.log",
	}
	stale := arcreviewstate.Session{
		ID:           "session-stale",
		PRID:         "ARCADIA-302",
		Workspace:    "/repo/workspaces/pr-302",
		Branch:       "trunk",
		Status:       "running",
		PID:          302,
		Revision:     "r-stale",
		Heartbeat:    now.Add(-10 * time.Minute),
		FailureCount: 2,
		LogPath:      "/tmp/pr-302.log",
	}
	dead := arcreviewstate.Session{
		ID:           "session-dead",
		PRID:         "ARCADIA-303",
		Workspace:    "/repo/workspaces/pr-303",
		Branch:       "trunk",
		Status:       "running",
		PID:          303,
		Revision:     "r-dead",
		Heartbeat:    now.Add(-1 * time.Minute),
		FailureCount: 3,
		LogPath:      "/tmp/pr-303.log",
	}
	for _, session := range []arcreviewstate.Session{fresh, stale, dead} {
		if _, err := store.CreateSession(session); err != nil {
			t.Fatalf("CreateSession(%s) error = %v", session.ID, err)
		}
	}

	started := []arcReviewProcessSpec{}
	restarted, err := restartStaleArcReviewSessions(store, arcReviewWatchCommandConfig{repoRoot: "/repo/yolo"}, arcReviewWatchConfig{
		StatePath:    "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
		PollInterval: 5 * time.Minute,
	}, now, arcReviewPIDLivenessFunc(func(pid int) bool {
		return pid != 303
	}), arcReviewProcessStarterFunc(func(spec arcReviewProcessSpec) (arcReviewStartedProcess, error) {
		started = append(started, spec)
		return arcReviewStartedProcess{PID: 9000 + len(started)}, nil
	}))
	if err != nil {
		t.Fatalf("restartStaleArcReviewSessions() error = %v", err)
	}
	if restarted != 2 {
		t.Fatalf("restartStaleArcReviewSessions() = %d, want 2", restarted)
	}

	gotFresh, err := store.GetSession("session-fresh")
	if err != nil {
		t.Fatalf("GetSession(fresh) error = %v", err)
	}
	if gotFresh != fresh {
		t.Fatalf("fresh session changed\ngot:  %#v\nwant: %#v", gotFresh, fresh)
	}

	gotStale, err := store.GetSession("session-stale")
	if err != nil {
		t.Fatalf("GetSession(stale) error = %v", err)
	}
	if gotStale.Status != "crashed" || gotStale.PID != 0 || gotStale.FailureCount != stale.FailureCount+1 {
		t.Fatalf("stale crashed session mismatch: %#v", gotStale)
	}
	gotDead, err := store.GetSession("session-dead")
	if err != nil {
		t.Fatalf("GetSession(dead) error = %v", err)
	}
	if gotDead.Status != "crashed" || gotDead.PID != 0 || gotDead.FailureCount != dead.FailureCount+1 {
		t.Fatalf("dead crashed session mismatch: %#v", gotDead)
	}

	gotReplacementStale, err := store.GetSession("pr-arcadia-302-2")
	if err != nil {
		t.Fatalf("GetSession(stale replacement) error = %v", err)
	}
	if gotReplacementStale.Status != "running" ||
		gotReplacementStale.PID != 9002 ||
		gotReplacementStale.FailureCount != gotStale.FailureCount ||
		gotReplacementStale.Workspace != stale.Workspace ||
		gotReplacementStale.Branch != stale.Branch ||
		gotReplacementStale.Revision != stale.Revision {
		t.Fatalf("stale replacement mismatch: %#v", gotReplacementStale)
	}
	gotReplacementDead, err := store.GetSession("pr-arcadia-303-2")
	if err != nil {
		t.Fatalf("GetSession(dead replacement) error = %v", err)
	}
	if gotReplacementDead.Status != "running" ||
		gotReplacementDead.PID != 9001 ||
		gotReplacementDead.FailureCount != gotDead.FailureCount ||
		gotReplacementDead.Workspace != dead.Workspace ||
		gotReplacementDead.Branch != dead.Branch ||
		gotReplacementDead.Revision != dead.Revision {
		t.Fatalf("dead replacement mismatch: %#v", gotReplacementDead)
	}

	wantStarted := []arcReviewProcessSpec{
		buildArcReviewProcessSpec(arcReviewProcessConfig{
			RepoRoot:   "/repo/yolo",
			Workspace:  dead.Workspace,
			PRID:       dead.PRID,
			SessionID:  "pr-arcadia-303-2",
			StatePath:  "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
			EventsPath: filepath.Join("/repo/yolo", "runner-logs", "arc-review-watch.events.jsonl"),
		}),
		buildArcReviewProcessSpec(arcReviewProcessConfig{
			RepoRoot:   "/repo/yolo",
			Workspace:  stale.Workspace,
			PRID:       stale.PRID,
			SessionID:  "pr-arcadia-302-2",
			StatePath:  "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
			EventsPath: filepath.Join("/repo/yolo", "runner-logs", "arc-review-watch.events.jsonl"),
		}),
	}
	if !reflect.DeepEqual(started, wantStarted) {
		t.Fatalf("started specs mismatch\ngot:  %#v\nwant: %#v", started, wantStarted)
	}

	restartedAgain, err := restartStaleArcReviewSessions(store, arcReviewWatchCommandConfig{repoRoot: "/repo/yolo"}, arcReviewWatchConfig{
		StatePath:    "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
		PollInterval: 5 * time.Minute,
	}, now, arcReviewPIDLivenessFunc(func(pid int) bool {
		return true
	}), arcReviewProcessStarterFunc(func(spec arcReviewProcessSpec) (arcReviewStartedProcess, error) {
		t.Fatalf("unexpected second start: %#v", spec)
		return arcReviewStartedProcess{}, nil
	}))
	if err != nil {
		t.Fatalf("second restartStaleArcReviewSessions() error = %v", err)
	}
	if restartedAgain != 0 {
		t.Fatalf("second restartStaleArcReviewSessions() = %d, want 0", restartedAgain)
	}
}

func TestRestartStaleArcReviewSessionsCreatesReplacementBeforeStartAndTargetsSession(t *testing.T) {
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, 6, 9, 16, 0, 0, 0, time.UTC)
	workspace := "/repo/workspaces/pr-401"
	for _, session := range []arcreviewstate.Session{
		{
			ID:        "pr-arcadia-401",
			PRID:      "ARCADIA-401",
			Workspace: workspace,
			Branch:    "trunk",
			Status:    "crashed",
			Revision:  "r-old",
		},
		{
			ID:        "pr-arcadia-401-2",
			PRID:      "ARCADIA-401",
			Workspace: workspace,
			Branch:    "trunk",
			Status:    "running",
			PID:       401,
			Revision:  "r-stale",
			Heartbeat: now.Add(-10 * time.Minute),
		},
	} {
		if _, err := store.CreateSession(session); err != nil {
			t.Fatalf("CreateSession(%s) error = %v", session.ID, err)
		}
	}

	restarted, err := restartStaleArcReviewSessions(store, arcReviewWatchCommandConfig{repoRoot: "/repo/yolo"}, arcReviewWatchConfig{
		StatePath:    "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
		PollInterval: 5 * time.Minute,
		Reviewer:     "alice",
		AllowShip:    true,
	}, now, arcReviewPIDLivenessFunc(func(int) bool {
		return true
	}), arcReviewProcessStarterFunc(func(spec arcReviewProcessSpec) (arcReviewStartedProcess, error) {
		replacement, err := store.GetSession("pr-arcadia-401-3")
		if err != nil {
			t.Fatalf("replacement session should exist before process start: %v", err)
		}
		if replacement.Status != "running" || replacement.PID != 0 || replacement.Workspace != workspace {
			t.Fatalf("replacement before start mismatch: %#v", replacement)
		}
		if !containsOrderedArgs(spec.Argv, "--session-id", "pr-arcadia-401-3") {
			t.Fatalf("started argv does not target replacement session: %#v", spec.Argv)
		}
		if !containsArg(spec.Argv, "--allow-ship=true") {
			t.Fatalf("started argv does not include allow_ship handoff: %#v", spec.Argv)
		}
		if !containsOrderedArgs(spec.Argv, "--reviewer", "alice") {
			t.Fatalf("started argv does not include reviewer handoff: %#v", spec.Argv)
		}
		return arcReviewStartedProcess{PID: 9401}, nil
	}))
	if err != nil {
		t.Fatalf("restartStaleArcReviewSessions() error = %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restartStaleArcReviewSessions() = %d, want 1", restarted)
	}

	replacement, err := store.GetSession("pr-arcadia-401-3")
	if err != nil {
		t.Fatalf("GetSession(replacement) error = %v", err)
	}
	if replacement.Status != "running" || replacement.PID != 9401 || replacement.Workspace != workspace {
		t.Fatalf("replacement after start mismatch: %#v", replacement)
	}
}

func TestRestartStaleArcReviewSessionsPassesWatcherHandoffToChildProcess(t *testing.T) {
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	session := arcreviewstate.Session{
		ID:        "pr-arcadia-501",
		PRID:      "ARCADIA-501",
		Workspace: "/repo/workspaces/pr-501",
		Branch:    "trunk",
		Status:    "running",
		PID:       501,
		Heartbeat: now.Add(-10 * time.Minute),
	}
	if _, err := store.CreateSession(session); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	var started []arcReviewProcessSpec
	restarted, err := restartStaleArcReviewSessions(store, arcReviewWatchCommandConfig{repoRoot: "/repo/yolo"}, arcReviewWatchConfig{
		StatePath:    "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
		PollInterval: 5 * time.Minute,
		Reviewer:     "  alice  ",
		AllowShip:    true,
	}, now, arcReviewPIDLivenessFunc(func(int) bool {
		return true
	}), arcReviewProcessStarterFunc(func(spec arcReviewProcessSpec) (arcReviewStartedProcess, error) {
		started = append(started, spec)
		return arcReviewStartedProcess{PID: 9501}, nil
	}))
	if err != nil {
		t.Fatalf("restartStaleArcReviewSessions() error = %v", err)
	}
	if restarted != 1 {
		t.Fatalf("restartStaleArcReviewSessions() = %d, want 1", restarted)
	}
	if len(started) != 1 {
		t.Fatalf("started %d child processes, want 1", len(started))
	}
	if !containsArg(started[0].Argv, "--allow-ship=true") {
		t.Fatalf("started argv missing allow_ship handoff: %#v", started[0].Argv)
	}
	if !containsOrderedArgs(started[0].Argv, "--reviewer", "alice") {
		t.Fatalf("started argv missing reviewer handoff: %#v", started[0].Argv)
	}
}

func TestArcPRReviewRunnerCommandParsesWatcherHandoff(t *testing.T) {
	originalRun := runArcPRReviewRunner
	t.Cleanup(func() {
		runArcPRReviewRunner = originalRun
	})

	var captured []arcPRReviewRunnerCommandConfig
	runArcPRReviewRunner = func(_ context.Context, cfg arcPRReviewRunnerCommandConfig) error {
		captured = append(captured, cfg)
		return nil
	}

	code := RunMain([]string{
		"arc-pr-review-runner",
		"--repo", "/repo/yolo",
		"--workspace", "/repo/workspaces/pr-502",
		"--pr", "ARCADIA-502",
		"--state", "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
		"--allow-ship=true",
		"--reviewer", "  alice  ",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0", code)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d runner configs, want 1", len(captured))
	}
	if !arcPRReviewRunnerConfigBoolField(t, captured[0], "allowShip") {
		t.Fatalf("allowShip = false, want true")
	}
	if got := arcPRReviewRunnerConfigStringField(t, captured[0], "reviewer"); got != "alice" {
		t.Fatalf("reviewer = %q, want alice", got)
	}

	captured = nil
	code = RunMain([]string{
		"arc-pr-review-runner",
		"--repo", "/repo/yolo",
		"--workspace", "/repo/workspaces/pr-503",
		"--pr", "ARCADIA-503",
		"--state", "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain() exit code without allow_ship = %d, want 0", code)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d default runner configs, want 1", len(captured))
	}
	if arcPRReviewRunnerConfigBoolField(t, captured[0], "allowShip") {
		t.Fatalf("allowShip default = true, want false")
	}
	if got := arcPRReviewRunnerConfigStringField(t, captured[0], "reviewer"); got != "" {
		t.Fatalf("reviewer default = %q, want empty", got)
	}
}

func containsOrderedArgs(args []string, key string, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func arcPRReviewRunnerConfigBoolField(t *testing.T, cfg arcPRReviewRunnerCommandConfig, name string) bool {
	t.Helper()
	field := reflect.ValueOf(cfg).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("arcPRReviewRunnerCommandConfig missing %s field", name)
	}
	if field.Kind() != reflect.Bool {
		t.Fatalf("arcPRReviewRunnerCommandConfig.%s kind = %s, want bool", name, field.Kind())
	}
	return field.Bool()
}

func arcPRReviewRunnerConfigStringField(t *testing.T, cfg arcPRReviewRunnerCommandConfig, name string) string {
	t.Helper()
	field := reflect.ValueOf(cfg).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("arcPRReviewRunnerCommandConfig missing %s field", name)
	}
	if field.Kind() != reflect.String {
		t.Fatalf("arcPRReviewRunnerCommandConfig.%s kind = %s, want string", name, field.Kind())
	}
	return field.String()
}
