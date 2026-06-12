package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		materialize: func(context.Context, envpreset.Preset, string) (envpreset.Workspace, error) {
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

type runnerImplementFakeVCS struct {
	ensureMainCalled bool
	checkedOut       string
}

func (v *runnerImplementFakeVCS) EnsureMain(context.Context) error {
	v.ensureMainCalled = true
	return nil
}

func (v *runnerImplementFakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
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
