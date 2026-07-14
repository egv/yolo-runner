package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerImplementHandlerWritesExecutorResultThroughQueue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	payload := marshalRunnerImplementPayload(t, workitem.ImplementPayload{
		TaskID:      "TASK-implement",
		Title:       "Implement queued task",
		Description: "Run the executor from the implement kind handler.",
		PromptContext: workitem.ImplementPromptContext{
			Prompt:   "Keep the change scoped.",
			ParentID: "ROOT-1",
			Metadata: map[string]string{"source": "test"},
		},
		BaseBranch: "main",
	})
	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         "test-source",
		SourceRef:      "TASK-implement",
		IdempotencyKey: "test-source/TASK-implement/implement",
		Preset:         "linux",
		Payload:        payload,
		MaxAttempts:    3,
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("runner registry Close() error = %v", err)
		}
	})
	if err := runners.Register("runner-implement-test", []string{"linux"}, 1); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	workspacePath := t.TempDir()
	fakeRunner := &runnerImplementFakeAgent{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultFailed, Reason: "first implementation failed"},
		{Status: contracts.RunnerResultFailed, Reason: "second implementation failed"},
		{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"commit_sha": "impl-sha"}},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true, Artifacts: map[string]string{"review_verdict": "pass"}},
	}}
	fakeVCS := &runnerImplementFakeVCS{}
	cleanupCalled := false
	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		handlers: runnerKindRegistry{
			workitem.KindImplement: newRunnerImplementKindHandler(func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
				return runnerImplementExecutor{
					Runner: fakeRunner,
					Agent: envpreset.ResolvedAgent{
						Backend:          "fake",
						Model:            "gpt-implement-test",
						RunnerTimeout:    3 * time.Second,
						WatchdogTimeout:  7 * time.Second,
						WatchdogInterval: time.Second,
					},
					Landing: envpreset.LandingTypeNone,
				}, nil
			}),
		},
		environmentPresets: map[string]envpreset.Preset{
			"linux": {
				Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath},
				Landing:   envpreset.Landing{Type: envpreset.LandingTypeNone},
			},
		},
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{
				Path: workspacePath,
				VCS:  fakeVCS,
				Cleanup: func() error {
					cleanupCalled = true
					return nil
				},
			}, nil
		},
		cfg: runnerDaemonCommandConfig{
			presets:           []string{"linux"},
			runnerID:          "runner-implement-test",
			once:              true,
			pollInterval:      time.Millisecond,
			heartbeatInterval: time.Hour,
			leaseTTL:          time.Minute,
		},
	}

	if err := daemon.Run(context.Background()); err != nil {
		t.Fatalf("daemon Run() error = %v", err)
	}

	results, err := store.ListUnconsumedResults("test-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	got := results[0]
	if got.Item.State != "done" {
		t.Fatalf("item state = %q, want done", got.Item.State)
	}
	if got.Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", got.Result.Status)
	}

	implementResult, err := workitem.DecodeImplementResult(got.Result.Payload)
	if err != nil {
		t.Fatalf("DecodeImplementResult(%s) error = %v", got.Result.Payload, err)
	}
	if implementResult.Status != string(contracts.RunnerResultCompleted) {
		t.Fatalf("implement status = %q, want completed", implementResult.Status)
	}
	if implementResult.Branch != "task/TASK-implement" {
		t.Fatalf("implement branch = %q, want task/TASK-implement", implementResult.Branch)
	}
	if implementResult.CommitSHA != "impl-sha" {
		t.Fatalf("implement commit sha = %q, want impl-sha", implementResult.CommitSHA)
	}
	if implementResult.ReviewVerdict != "pass" {
		t.Fatalf("implement review verdict = %q, want pass", implementResult.ReviewVerdict)
	}

	if len(fakeRunner.requests) != 4 {
		t.Fatalf("fake runner requests = %d, want 4 including review", len(fakeRunner.requests))
	}
	implementRequests := 0
	reviewRequests := 0
	for _, request := range fakeRunner.requests {
		switch request.Mode {
		case contracts.RunnerModeImplement:
			implementRequests++
		case contracts.RunnerModeReview:
			reviewRequests++
		default:
			t.Fatalf("runner mode = %q, want implement or review", request.Mode)
		}
		if request.Model != "gpt-implement-test" {
			t.Fatalf("runner model = %q, want gpt-implement-test", request.Model)
		}
		if request.RepoRoot != workspacePath {
			t.Fatalf("runner repo root = %q, want %q", request.RepoRoot, workspacePath)
		}
		if request.Timeout != 3*time.Second {
			t.Fatalf("runner timeout = %s, want 3s", request.Timeout)
		}
	}
	if implementRequests != 3 {
		t.Fatalf("implement runner requests = %d, want 3 from item max_attempts retry budget", implementRequests)
	}
	if reviewRequests != 1 {
		t.Fatalf("review runner requests = %d, want 1", reviewRequests)
	}
	if !strings.Contains(fakeRunner.requests[1].Prompt, "first implementation failed") {
		t.Fatalf("second prompt does not include first failure feedback: %q", fakeRunner.requests[1].Prompt)
	}
	if !strings.Contains(fakeRunner.requests[2].Prompt, "second implementation failed") {
		t.Fatalf("third prompt does not include second failure feedback: %q", fakeRunner.requests[2].Prompt)
	}
	if !fakeVCS.ensureMainCalled {
		t.Fatal("expected executor to prepare VCS main branch")
	}
	if fakeVCS.checkedOut != "task/TASK-implement" {
		t.Fatalf("checked out branch = %q, want task/TASK-implement", fakeVCS.checkedOut)
	}
	if !cleanupCalled {
		t.Fatal("expected materialized workspace cleanup")
	}
}

func TestRunnerImplementResolverForPresetsPreservesEventSink(t *testing.T) {
	eventSink := &runnerImplementRecordingEventSink{}
	resolver := newRunnerImplementExecutorResolverForPresets(map[string]envpreset.Preset{
		"dogfood": {
			Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath, Path: t.TempDir()},
			Landing:   envpreset.Landing{Type: envpreset.LandingTypeNone},
			Agent: envpreset.Agent{
				Backend: "claude",
				Model:   "fixture",
			},
		},
	}, eventSink)

	resolved, err := resolver(context.Background(), workitem.Item{Preset: "dogfood"}, envpreset.Workspace{})
	if err != nil {
		t.Fatalf("resolver returned error: %v", err)
	}
	if resolved.Events != eventSink {
		t.Fatalf("resolver dropped event sink: got %#v want %#v", resolved.Events, eventSink)
	}
}

func marshalRunnerImplementPayload(t *testing.T, payload workitem.ImplementPayload) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal implement payload: %v", err)
	}
	return raw
}

type runnerImplementFakeAgent struct {
	results  []contracts.RunnerResult
	requests []contracts.RunnerRequest
}

func (f *runnerImplementFakeAgent) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if len(f.results) == 0 {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

type runnerImplementRecordingEventSink struct{}

func (*runnerImplementRecordingEventSink) Emit(context.Context, contracts.Event) error {
	return nil
}

type runnerImplementFakeVCS struct {
	ensureMainCalled       bool
	checkedOut             string
	createTaskBranchCalled bool
	checkoutPRBranchPR     string
	pushPRBranchPR         string
}

func (v *runnerImplementFakeVCS) EnsureMain(context.Context) error {
	v.ensureMainCalled = true
	return nil
}

func (v *runnerImplementFakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	v.createTaskBranchCalled = true
	return "task/" + taskID, nil
}

func (v *runnerImplementFakeVCS) Checkout(_ context.Context, ref string) error {
	v.checkedOut = ref
	return nil
}

func (v *runnerImplementFakeVCS) CommitAll(context.Context, string) (string, error) {
	return "", nil
}

func (v *runnerImplementFakeVCS) MergeToMain(context.Context, string) error {
	return nil
}

func (v *runnerImplementFakeVCS) PushBranch(context.Context, string) error {
	return nil
}

func (v *runnerImplementFakeVCS) PushMain(context.Context) error {
	return nil
}

func (v *runnerImplementFakeVCS) CheckoutPRBranch(_ context.Context, prID string) (string, error) {
	v.checkoutPRBranchPR = prID
	if strings.TrimSpace(prID) == "" {
		return "pr-branch", nil
	}
	return "pr-" + prID, nil
}

func (v *runnerImplementFakeVCS) PushPRBranch(_ context.Context, prID string) error {
	v.pushPRBranchPR = prID
	return nil
}

func TestRunnerImplementHandlerRejectsNonIsolatedWorkspace(t *testing.T) {
	handler := newRunnerImplementKindHandler(func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
		t.Fatal("resolver must not run when the workspace has no VCS")
		return runnerImplementExecutor{}, nil
	})

	payload, err := json.Marshal(workitem.ImplementPayload{TaskID: "T-1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	item := workitem.Item{ID: "item-no-vcs", Kind: workitem.KindImplement, Preset: "linux", Payload: payload}

	// Path-strategy / read-only workspaces have no VCS; a code-writing item must
	// be rejected rather than silently run in the live tree without landing.
	_, err = handler(context.Background(), item, envpreset.Workspace{Path: t.TempDir(), VCS: nil})
	if err == nil {
		t.Fatal("expected error for implement item with no VCS-bearing workspace")
	}
	if !strings.Contains(err.Error(), "isolated") {
		t.Fatalf("error should explain the isolation requirement, got: %v", err)
	}
}

func TestResolveRunnerImplementPRLanding(t *testing.T) {
	t.Run("non-author is disabled", func(t *testing.T) {
		got, err := resolveRunnerImplementPRLanding(workitem.ImplementPayload{
			PromptContext: workitem.ImplementPromptContext{Metadata: map[string]string{"origin": "startrek"}},
		}, "item-1")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.enabled {
			t.Fatal("non-author item must not enable PR landing")
		}
	})

	t.Run("author without arc_pr_id errors", func(t *testing.T) {
		if _, err := resolveRunnerImplementPRLanding(workitem.ImplementPayload{
			PromptContext: workitem.ImplementPromptContext{Metadata: map[string]string{"origin": "arcpr-author"}},
		}, "item-1"); err == nil {
			t.Fatal("expected error for arcpr-author item missing arc_pr_id")
		}
	})

	t.Run("author uses seam checkout and cleans up", func(t *testing.T) {
		prev := runnerImplementPreparePRCheckout
		t.Cleanup(func() { runnerImplementPreparePRCheckout = prev })
		cleanupCalled := false
		runnerImplementPreparePRCheckout = func(prID string) (*arcanum.PRCheckout, error) {
			if prID != "123" {
				t.Fatalf("seam prID = %q, want 123", prID)
			}
			return &arcanum.PRCheckout{MountPath: "/tmp/pr-mount", Cleanup: func() error { cleanupCalled = true; return nil }}, nil
		}
		got, err := resolveRunnerImplementPRLanding(workitem.ImplementPayload{
			PromptContext: workitem.ImplementPromptContext{Metadata: map[string]string{"origin": "arcpr-author", "arc_pr_id": "123"}},
		}, "item-1")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !got.enabled || got.prID != "123" || got.mountPath != "/tmp/pr-mount" {
			t.Fatalf("landing = %+v", got)
		}
		got.cleanup(context.Background())
		if !cleanupCalled {
			t.Fatal("checkout Cleanup must be wired through")
		}
	})
}

// Landing applicability: an author-mode implement item re-checks its comment
// against the live PR right before landing. A comment that is resolved,
// deleted, or already answered by the PR author must be skipped without
// preparing a checkout or running the agent.
func TestRunnerImplementSkipsObsoleteAuthorComment(t *testing.T) {
	cases := []struct {
		name       string
		comments   []arcreview.PRComment
		wantReason string
	}{
		{
			name: "comment already resolved",
			comments: []arcreview.PRComment{
				{ID: "c-1", Body: "please fix", Resolved: true},
			},
			wantReason: "already resolved",
		},
		{
			name:       "comment deleted",
			comments:   []arcreview.PRComment{{ID: "c-other", Body: "unrelated"}},
			wantReason: "no longer exists",
		},
		{
			name: "author already replied in thread",
			comments: []arcreview.PRComment{
				{ID: "c-1", Body: "please fix"},
				{ID: "c-9", ThreadID: "c-1", Author: "alice", Body: "Fixed in `abc`."},
			},
			wantReason: "already answered by alice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevFetch := runnerImplementFetchPRComments
			prevPrepare := runnerImplementPreparePRCheckout
			t.Cleanup(func() {
				runnerImplementFetchPRComments = prevFetch
				runnerImplementPreparePRCheckout = prevPrepare
			})
			fetchedPR := ""
			runnerImplementFetchPRComments = func(_ context.Context, prID string) ([]arcreview.PRComment, error) {
				fetchedPR = prID
				return tc.comments, nil
			}
			runnerImplementPreparePRCheckout = func(string) (*arcanum.PRCheckout, error) {
				t.Fatal("prepare checkout must not run for an obsolete comment")
				return nil, nil
			}

			handler := newRunnerImplementKindHandler(func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
				t.Fatal("executor must not be resolved for an obsolete comment")
				return runnerImplementExecutor{}, nil
			})
			item := workitem.Item{
				ID:   "item-obsolete",
				Kind: workitem.KindImplement,
				Payload: marshalRunnerImplementPayload(t, workitem.ImplementPayload{
					TaskID: "PR-42-c-1",
					Title:  "Fix comment c-1",
					PromptContext: workitem.ImplementPromptContext{
						Prompt: "Apply the fix.",
						Metadata: map[string]string{
							"origin":         "arcpr-author",
							"arc_pr_id":      "42",
							"arc_comment_id": "c-1",
							"arc_pr_author":  "alice",
						},
					},
				}),
			}

			result, err := handler(context.Background(), item, envpreset.Workspace{})
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if fetchedPR != "42" {
				t.Fatalf("fetched comments for PR %q, want 42", fetchedPR)
			}
			if result.Status != workqueue.ResultStatusCompleted {
				t.Fatalf("result status = %q, want completed", result.Status)
			}
			implementResult, err := workitem.DecodeImplementResult(result.Payload)
			if err != nil {
				t.Fatalf("DecodeImplementResult() error = %v", err)
			}
			if implementResult.Status != runnerImplementSkippedStatus {
				t.Fatalf("implement result status = %q, want %q", implementResult.Status, runnerImplementSkippedStatus)
			}
			if !strings.Contains(implementResult.Reason, tc.wantReason) {
				t.Fatalf("implement result reason = %q, want it to contain %q", implementResult.Reason, tc.wantReason)
			}
		})
	}
}

// A live comment — open thread, replies only from reviewers — must pass the
// applicability gate and proceed to the normal landing path.
func TestRunnerImplementProceedsWhenAuthorCommentStillApplicable(t *testing.T) {
	prevFetch := runnerImplementFetchPRComments
	prevPrepare := runnerImplementPreparePRCheckout
	t.Cleanup(func() {
		runnerImplementFetchPRComments = prevFetch
		runnerImplementPreparePRCheckout = prevPrepare
	})
	runnerImplementFetchPRComments = func(context.Context, string) ([]arcreview.PRComment, error) {
		return []arcreview.PRComment{
			{ID: "c-1", Body: "please fix"},
			{ID: "c-9", ThreadID: "c-1", Author: "reviewer-bob", Body: "still waiting"},
		}, nil
	}
	prepareCalled := false
	sentinel := errors.New("prepare checkout sentinel")
	runnerImplementPreparePRCheckout = func(prID string) (*arcanum.PRCheckout, error) {
		prepareCalled = true
		if prID != "42" {
			t.Fatalf("prepare checkout PR = %q, want 42", prID)
		}
		return nil, sentinel
	}

	handler := newRunnerImplementKindHandler(func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
		return runnerImplementExecutor{}, nil
	})
	item := workitem.Item{
		ID:   "item-live",
		Kind: workitem.KindImplement,
		Payload: marshalRunnerImplementPayload(t, workitem.ImplementPayload{
			TaskID: "PR-42-c-1",
			Title:  "Fix comment c-1",
			PromptContext: workitem.ImplementPromptContext{
				Prompt: "Apply the fix.",
				Metadata: map[string]string{
					"origin":         "arcpr-author",
					"arc_pr_id":      "42",
					"arc_comment_id": "c-1",
					"arc_pr_author":  "alice",
				},
			},
		}),
	}

	_, err := handler(context.Background(), item, envpreset.Workspace{})
	if !prepareCalled {
		t.Fatal("prepare checkout was not reached for a still-applicable comment")
	}
	if err == nil || !strings.Contains(err.Error(), sentinel.Error()) {
		t.Fatalf("handler() error = %v, want the prepare checkout sentinel", err)
	}
}

func TestRunnerImplementAuthorModeLandsOnExistingPR(t *testing.T) {
	// Override PreparePRCheckout so author-mode items use an in-process mount
	// path instead of a real arc mount.
	prMount := t.TempDir()
	prev := runnerImplementPreparePRCheckout
	t.Cleanup(func() { runnerImplementPreparePRCheckout = prev })
	runnerImplementPreparePRCheckout = func(string) (*arcanum.PRCheckout, error) {
		return &arcanum.PRCheckout{MountPath: prMount, Cleanup: func() error { return nil }}, nil
	}
	fakeVCS := &runnerImplementFakeVCS{}
	previousPRVCS := runnerImplementPRVCS
	t.Cleanup(func() { runnerImplementPRVCS = previousPRVCS })
	runnerImplementPRVCS = func(path string) contracts.VCS {
		if path != prMount {
			t.Fatalf("PR VCS path = %q, want %q", path, prMount)
		}
		return fakeVCS
	}

	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	payload := marshalRunnerImplementPayload(t, workitem.ImplementPayload{
		TaskID:      "PR-999-fix",
		Title:       "Fix PR comment",
		Description: "Author-mode fix spawned from a PR comment.",
		PromptContext: workitem.ImplementPromptContext{
			Prompt:   "Apply the reviewer's valid suggestion.",
			ParentID: "yolo-g8e",
			Metadata: map[string]string{"origin": "arcpr-author", "arc_pr_id": "PR-999"},
		},
		BaseBranch: "main",
	})
	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         "arcpr",
		SourceRef:      "pr:PR-999",
		IdempotencyKey: "arcpr/PR-999/implement/PR-999-fix",
		Preset:         "arcpr",
		Payload:        payload,
		MaxAttempts:    3,
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("runner registry Close() error = %v", err)
		}
	})
	if err := runners.Register("runner-arcpr-test", []string{"arcpr"}, 1); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	fakeRunner := &runnerImplementFakeAgent{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"commit_sha": "pr-fix-sha"}},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true, Artifacts: map[string]string{"review_verdict": "pass"}},
	}}
	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		handlers: runnerKindRegistry{
			workitem.KindImplement: newRunnerImplementKindHandler(func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
				return runnerImplementExecutor{
					Runner: fakeRunner,
					Agent: envpreset.ResolvedAgent{
						Backend:          "fake",
						Model:            "m",
						RunnerTimeout:    3 * time.Second,
						WatchdogTimeout:  7 * time.Second,
						WatchdogInterval: time.Second,
					},
					Landing: envpreset.LandingTypeGitMerge,
				}, nil
			}),
		},
		environmentPresets: map[string]envpreset.Preset{
			"arcpr": {
				Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath},
				Landing:   envpreset.Landing{Type: envpreset.LandingTypeGitMerge},
			},
		},
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			t.Fatal("author-mode implement item must prepare its own PR checkout")
			return envpreset.Workspace{}, nil
		},
		cfg: runnerDaemonCommandConfig{
			presets:           []string{"arcpr"},
			runnerID:          "runner-arcpr-test",
			once:              true,
			pollInterval:      time.Millisecond,
			heartbeatInterval: time.Hour,
			leaseTTL:          time.Minute,
		},
	}

	if err := daemon.Run(context.Background()); err != nil {
		t.Fatalf("daemon Run() error = %v", err)
	}

	results, err := store.ListUnconsumedResults("arcpr")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults len = %d, want 1", len(results))
	}
	if results[0].Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", results[0].Result.Status)
	}

	// Author-mode implement items must reuse the existing PR branch (via
	// CheckoutPRBranch) and land by force-pushing it — never CreateTaskBranch.
	if fakeVCS.createTaskBranchCalled {
		t.Fatal("CreateTaskBranch must NOT be called for arcpr-author implement items")
	}
	if fakeVCS.checkoutPRBranchPR != "PR-999" {
		t.Fatalf("CheckoutPRBranch pr = %q, want PR-999", fakeVCS.checkoutPRBranchPR)
	}
	if fakeVCS.pushPRBranchPR != "PR-999" {
		t.Fatalf("PushPRBranch pr = %q, want PR-999", fakeVCS.pushPRBranchPR)
	}
}
