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

func TestRunnerFinalizeHandlerCreatesParentPRResult(t *testing.T) {
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

	payload, err := json.Marshal(workitem.FinalizePayload{
		ParentRef:     "VAY-42",
		ChildBranches: []string{"task/VAY-43", "task/VAY-44"},
		Title:         "Parent split task",
	})
	if err != nil {
		t.Fatalf("marshal finalize payload: %v", err)
	}
	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindFinalize,
		Source:         "startrek",
		SourceRef:      "VAY-42",
		IdempotencyKey: "st/VAY-42/finalize/rev7",
		Preset:         "arc",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	item, err := store.Claim("runner-finalize-test", []string{"arc"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if item == nil {
		t.Fatal("Claim() returned nil item")
	}

	vcs := &runnerFinalizeFakeVCS{prURL: "https://arc.example.test/review/parent"}
	daemon := runnerDaemon{
		store:    store,
		handlers: defaultRunnerKindRegistry(),
		environmentPresets: map[string]envpreset.Preset{
			"arc": {
				Workspace: envpreset.Workspace{Strategy: envpreset.WorkspaceStrategyPath},
				Landing:   envpreset.Landing{Type: envpreset.LandingTypeArcPR},
			},
		},
		materialize: func(context.Context, envpreset.Preset, string) (envpreset.Workspace, error) {
			return envpreset.Workspace{
				Path: t.TempDir(),
				VCS:  vcs,
				Cleanup: func() error {
					return nil
				},
			}, nil
		},
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-finalize-test",
			heartbeatInterval: time.Hour,
		},
	}

	if err := daemon.runClaimedItem(context.Background(), *item); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}
	if len(vcs.createPRCalls) != 1 {
		t.Fatalf("CreatePR calls = %d, want 1", len(vcs.createPRCalls))
	}
	if call := vcs.createPRCalls[0]; !strings.Contains(call, "Parent split task") || !strings.Contains(call, "VAY-42") || !strings.Contains(call, "task/VAY-43") || !strings.Contains(call, "task/VAY-44") {
		t.Fatalf("CreatePR call missing finalize context: %q", call)
	}

	results, err := store.ListUnconsumedResults("startrek")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	if results[0].Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", results[0].Result.Status)
	}
	finalizeResult, err := workitem.DecodeFinalizeResult(results[0].Result.Payload)
	if err != nil {
		t.Fatalf("DecodeFinalizeResult(%s) error = %v", results[0].Result.Payload, err)
	}
	if finalizeResult.PRURL != "https://arc.example.test/review/parent" {
		t.Fatalf("finalize PR URL = %q, want https://arc.example.test/review/parent", finalizeResult.PRURL)
	}
}

type runnerFinalizeFakeVCS struct {
	prURL         string
	createPRCalls []string
}

func (v *runnerFinalizeFakeVCS) EnsureMain(context.Context) error { return nil }

func (v *runnerFinalizeFakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}

func (v *runnerFinalizeFakeVCS) Checkout(context.Context, string) error { return nil }

func (v *runnerFinalizeFakeVCS) CommitAll(context.Context, string) (string, error) {
	return "", nil
}

func (v *runnerFinalizeFakeVCS) MergeToMain(context.Context, string) error { return nil }

func (v *runnerFinalizeFakeVCS) PushBranch(context.Context, string) error { return nil }

func (v *runnerFinalizeFakeVCS) PushMain(context.Context) error { return nil }

func (v *runnerFinalizeFakeVCS) CreatePR(_ context.Context, title string, body string) (string, error) {
	v.createPRCalls = append(v.createPRCalls, title+"\n"+body)
	return v.prURL, nil
}

var _ contracts.VCS = (*runnerFinalizeFakeVCS)(nil)
