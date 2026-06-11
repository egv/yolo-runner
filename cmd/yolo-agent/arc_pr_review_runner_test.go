package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

func TestArcPRReviewRunnerCommandOnceWritesHeartbeat(t *testing.T) {
	repoRoot := t.TempDir()
	statePath := filepath.Join(repoRoot, ".yolo-runner", "arc-review-watch-state.db")
	sessionID := "pr-42"

	store, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	_, err = store.CreateSession(arcreviewstate.Session{
		ID:        sessionID,
		PRID:      "42",
		Workspace: filepath.Join(repoRoot, "workspaces", "pr-42"),
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	code := RunMain([]string{
		"arc-pr-review-runner",
		"--repo", repoRoot,
		"--workspace", filepath.Join(repoRoot, "workspaces", "pr-42"),
		"--pr", "42",
		"--state", statePath,
		"--events", filepath.Join(repoRoot, "runner-logs", "arc-pr-review.events.jsonl"),
		"--once",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0", code)
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

	heartbeat, err := reopened.GetHeartbeat(sessionID)
	if err != nil {
		t.Fatalf("GetHeartbeat() error = %v", err)
	}
	if heartbeat.IsZero() {
		t.Fatalf("expected heartbeat to be written")
	}
	if time.Since(heartbeat) > time.Minute {
		t.Fatalf("heartbeat is too old: %s", heartbeat)
	}
}

func TestArcPRReviewRunnerCommandParsesWatcherHandoffFlags(t *testing.T) {
	originalRun := runArcPRReviewRunner
	t.Cleanup(func() {
		runArcPRReviewRunner = originalRun
	})

	tests := []struct {
		name string
		args []string
		want arcPRReviewRunnerCommandConfig
	}{
		{
			name: "configured values",
			args: []string{
				"--repo", "/repo/yolo",
				"--workspace", "/repo/workspaces/pr-42",
				"--pr-id", "ARCADIA-42",
				"--session-id", "pr-arcadia-42",
				"--state-path", "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
				"--events", "/repo/yolo/runner-logs/arc-review-watch.events.jsonl",
				"--allow-ship=true",
				"--reviewer", "alice",
				"--once",
			},
			want: arcPRReviewRunnerCommandConfig{
				repoRoot:   "/repo/yolo",
				workspace:  "/repo/workspaces/pr-42",
				prID:       "ARCADIA-42",
				sessionID:  "pr-arcadia-42",
				statePath:  "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
				eventsPath: "/repo/yolo/runner-logs/arc-review-watch.events.jsonl",
				allowShip:  true,
				reviewer:   "alice",
				once:       true,
			},
		},
		{
			name: "allow ship defaults false when absent",
			args: []string{
				"--repo", "/repo/yolo",
				"--workspace", "/repo/workspaces/pr-43",
				"--pr-id", "ARCADIA-43",
				"--state-path", "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
				"--once",
			},
			want: arcPRReviewRunnerCommandConfig{
				repoRoot:  "/repo/yolo",
				workspace: "/repo/workspaces/pr-43",
				prID:      "ARCADIA-43",
				statePath: "/repo/yolo/.yolo-runner/arc-review-watch-state.db",
				once:      true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got arcPRReviewRunnerCommandConfig
			runArcPRReviewRunner = func(ctx context.Context, cfg arcPRReviewRunnerCommandConfig) error {
				if ctx == nil {
					t.Fatal("runArcPRReviewRunner() context is nil")
				}
				got = cfg
				return nil
			}
			if code := arcPRReviewRunnerCommand(tt.args); code != 0 {
				t.Fatalf("arcPRReviewRunnerCommand() exit code = %d, want 0", code)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("arcPRReviewRunnerCommand() config\ngot:  %#v\nwant: %#v", got, tt.want)
			}
		})
	}
}

func TestArcPRReviewRunnerCommandOnceWritesHeartbeatForExplicitSessionWhenWorkspaceHasHistory(t *testing.T) {
	repoRoot := t.TempDir()
	statePath := filepath.Join(repoRoot, ".yolo-runner", "arc-review-watch-state.db")
	workspace := filepath.Join(repoRoot, "workspaces", "pr-42")

	store, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for _, session := range []arcreviewstate.Session{
		{
			ID:        "pr-42",
			PRID:      "42",
			Workspace: workspace,
			Status:    "crashed",
		},
		{
			ID:        "pr-42-2",
			PRID:      "42",
			Workspace: workspace,
			Status:    "running",
		},
	} {
		if _, err := store.CreateSession(session); err != nil {
			t.Fatalf("CreateSession(%s) error = %v", session.ID, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	code := RunMain([]string{
		"arc-pr-review-runner",
		"--repo", repoRoot,
		"--workspace", workspace,
		"--pr", "42",
		"--session-id", "pr-42-2",
		"--state", statePath,
		"--events", filepath.Join(repoRoot, "runner-logs", "arc-pr-review.events.jsonl"),
		"--once",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0", code)
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

	oldHeartbeat, err := reopened.GetHeartbeat("pr-42")
	if err != nil {
		t.Fatalf("GetHeartbeat(old) error = %v", err)
	}
	if !oldHeartbeat.IsZero() {
		t.Fatalf("old session heartbeat changed: %s", oldHeartbeat)
	}
	replacementHeartbeat, err := reopened.GetHeartbeat("pr-42-2")
	if err != nil {
		t.Fatalf("GetHeartbeat(replacement) error = %v", err)
	}
	if replacementHeartbeat.IsZero() {
		t.Fatalf("expected replacement heartbeat to be written")
	}
}

func TestRunArcPRReviewRunnerLoopHeartbeatsAndExitsOnTerminalAction(t *testing.T) {
	times := []time.Time{
		time.Date(2026, 6, 11, 9, 30, 0, 0, time.UTC),
		time.Date(2026, 6, 11, 9, 30, 5, 0, time.UTC),
		time.Date(2026, 6, 11, 9, 30, 10, 0, time.UTC),
	}
	nowCalls := 0
	var heartbeats []time.Time
	var waits []time.Duration
	var cycles []arcPRReviewCycleConfig
	actions := []arcPRReviewRunnerCycleResult{
		{Action: arcreview.PRRunnerActionWait},
		{Action: arcreview.PRRunnerActionReview},
		{Action: arcreview.PRRunnerActionWait, Terminal: true},
	}

	err := runArcPRReviewRunnerLoop(context.Background(), arcPRReviewRunnerLoopConfig{
		CycleConfig:  arcPRReviewCycleConfig{PRID: "42", Workspace: "/workspace", RepoRoot: "/repo", AllowShip: true},
		PollInterval: 25 * time.Millisecond,
		Heartbeat: func(_ context.Context, at time.Time) error {
			heartbeats = append(heartbeats, at)
			return nil
		},
		Cycle: func(_ context.Context, cfg arcPRReviewCycleConfig) (arcPRReviewRunnerCycleResult, error) {
			cycles = append(cycles, cfg)
			return actions[len(cycles)-1], nil
		},
		Now: func() time.Time {
			if nowCalls >= len(times) {
				t.Fatalf("unexpected clock call %d", nowCalls+1)
			}
			at := times[nowCalls]
			nowCalls++
			return at
		},
		Wait: func(_ context.Context, interval time.Duration) error {
			waits = append(waits, interval)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runArcPRReviewRunnerLoop() error = %v", err)
	}
	if len(cycles) != 3 {
		t.Fatalf("expected 3 cycles, got %d", len(cycles))
	}
	for _, cycle := range cycles {
		if cycle.PRID != "42" || cycle.Workspace != "/workspace" || cycle.RepoRoot != "/repo" || !cycle.AllowShip {
			t.Fatalf("cycle config = %#v", cycle)
		}
	}
	if !reflect.DeepEqual(heartbeats, times) {
		t.Fatalf("heartbeats = %#v, want %#v", heartbeats, times)
	}
	if !reflect.DeepEqual(waits, []time.Duration{25 * time.Millisecond, 25 * time.Millisecond}) {
		t.Fatalf("waits = %#v", waits)
	}
}

func TestRunArcPRReviewRunnerLoopRefreshesShipGateInputFromConfigEachCycle(t *testing.T) {
	resolver := &fakeArcPRReviewRunnerReviewWatchConfigResolver{
		configs: []arcReviewWatchConfig{
			{AllowShip: false, Reviewer: "alice"},
			{AllowShip: true, Reviewer: "bob"},
		},
	}
	shipGate := &fakeArcPRReviewCycleShipGate{}
	var allowShipByCycle []bool
	var reviewerByCycle []string
	cycles := 0

	err := runArcPRReviewRunnerLoop(context.Background(), arcPRReviewRunnerLoopConfig{
		CycleConfig: arcPRReviewCycleConfig{
			PRID:      "42",
			Workspace: "/repo/workspaces/pr-42",
			RepoRoot:  "/repo/yolo",
			Metadata:  map[string]string{"phase": "arc_pr_review_cycle", "reviewer": "alice"},
			AllowShip: false,
			StateFetcher: &fakeArcPRReviewCycleFetcher{state: arcreview.PRRuntimeState{
				PRID:     "42",
				Revision: "r2",
				Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r2"},
				Checks: []arcreview.PRCheck{
					{Name: "ci", Status: "passed"},
				},
			}},
			RevisionStore: &fakeArcPRReviewCycleRevisionStore{revision: "r2"},
			ShipGate:      shipGate,
		},
		PollInterval:              25 * time.Millisecond,
		ReviewWatchConfigResolver: resolver,
		Heartbeat: func(context.Context, time.Time) error {
			return nil
		},
		Cycle: func(ctx context.Context, cfg arcPRReviewCycleConfig) (arcPRReviewRunnerCycleResult, error) {
			cycles++
			allowShipByCycle = append(allowShipByCycle, cfg.AllowShip)
			reviewerByCycle = append(reviewerByCycle, cfg.Metadata["reviewer"])
			result, err := defaultRunArcPRReviewRunnerCycle(ctx, cfg)
			if cycles == 2 {
				result.Terminal = true
			}
			return result, err
		},
		Now: func() time.Time {
			return time.Date(2026, 6, 11, 9, 30, 0, 0, time.UTC)
		},
		Wait: func(context.Context, time.Duration) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runArcPRReviewRunnerLoop() error = %v", err)
	}
	if !reflect.DeepEqual(allowShipByCycle, []bool{false, true}) {
		t.Fatalf("cycle allow_ship values = %#v, want %#v", allowShipByCycle, []bool{false, true})
	}
	if !reflect.DeepEqual(reviewerByCycle, []string{"alice", "bob"}) {
		t.Fatalf("cycle reviewers = %#v, want %#v", reviewerByCycle, []string{"alice", "bob"})
	}
	if len(shipGate.calls) != 1 {
		t.Fatalf("ship gate calls = %d, want 1", len(shipGate.calls))
	}
	if !shipGate.calls[0].AllowShip {
		t.Fatalf("ship gate allow_ship = false, want true")
	}
	if !reflect.DeepEqual(resolver.calls, []string{"/repo/yolo", "/repo/yolo"}) {
		t.Fatalf("resolver calls = %#v", resolver.calls)
	}
}

func TestRunArcPRReviewRunnerLoopFallsBackToSpawnConfigWhenRefreshFails(t *testing.T) {
	resolver := &fakeArcPRReviewRunnerReviewWatchConfigResolver{
		configs: []arcReviewWatchConfig{
			{AllowShip: false, Reviewer: "bob"},
		},
		errs: []error{nil, errors.New("reload failed")},
	}
	var allowShipByCycle []bool
	var reviewerByCycle []string
	cycles := 0

	err := runArcPRReviewRunnerLoop(context.Background(), arcPRReviewRunnerLoopConfig{
		CycleConfig: arcPRReviewCycleConfig{
			PRID:      "42",
			Workspace: "/repo/workspaces/pr-42",
			RepoRoot:  "/repo/yolo",
			Metadata:  map[string]string{"phase": "arc_pr_review_cycle", "reviewer": "alice"},
			AllowShip: true,
		},
		PollInterval:              25 * time.Millisecond,
		ReviewWatchConfigResolver: resolver,
		Heartbeat: func(context.Context, time.Time) error {
			return nil
		},
		Cycle: func(_ context.Context, cfg arcPRReviewCycleConfig) (arcPRReviewRunnerCycleResult, error) {
			cycles++
			allowShipByCycle = append(allowShipByCycle, cfg.AllowShip)
			reviewerByCycle = append(reviewerByCycle, cfg.Metadata["reviewer"])
			return arcPRReviewRunnerCycleResult{Terminal: cycles == 2}, nil
		},
		Now: func() time.Time {
			return time.Date(2026, 6, 11, 9, 30, 0, 0, time.UTC)
		},
		Wait: func(context.Context, time.Duration) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runArcPRReviewRunnerLoop() error = %v", err)
	}
	if !reflect.DeepEqual(allowShipByCycle, []bool{false, true}) {
		t.Fatalf("cycle allow_ship values = %#v, want %#v", allowShipByCycle, []bool{false, true})
	}
	if !reflect.DeepEqual(reviewerByCycle, []string{"bob", "alice"}) {
		t.Fatalf("cycle reviewers = %#v, want %#v", reviewerByCycle, []string{"bob", "alice"})
	}
}

type fakeArcPRReviewRunnerReviewWatchConfigResolver struct {
	configs []arcReviewWatchConfig
	errs    []error
	calls   []string
}

func (r *fakeArcPRReviewRunnerReviewWatchConfigResolver) ResolveArcReviewWatchConfig(repoRoot string) (arcReviewWatchConfig, error) {
	r.calls = append(r.calls, repoRoot)
	index := len(r.calls) - 1
	if index < len(r.errs) && r.errs[index] != nil {
		return arcReviewWatchConfig{}, r.errs[index]
	}
	if len(r.configs) == 0 {
		return arcReviewWatchConfig{}, nil
	}
	if index >= len(r.configs) {
		index = len(r.configs) - 1
	}
	return r.configs[index], nil
}
