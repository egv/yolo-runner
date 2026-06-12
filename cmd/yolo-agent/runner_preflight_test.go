package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerPreflightHandlerWritesReadyAndNeedsInfoResultsThroughQueue(t *testing.T) {
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

	readyPayload := marshalRunnerPreflightPayload(t, workitem.PreflightPayload{
		Task: workitem.TaskPayload{
			ID:          "TASK-ready",
			Title:       "Wire preflight handler",
			Description: "Run queue preflight in the runner.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "ROOT-1",
		},
		Comments: []workitem.PreflightComment{{Author: "alice", Body: "Looks scoped."}},
		QueueRoot: workitem.TaskPayload{
			ID:          "ROOT-1",
			Title:       "Queue split",
			Description: "Move model work into queue runners.",
			Status:      contracts.TaskStatusOpen,
		},
	})
	needsInfoPayload := marshalRunnerPreflightPayload(t, workitem.PreflightPayload{
		Task: workitem.TaskPayload{
			ID:          "TASK-needs-info",
			Title:       "Choose writeback owner",
			Description: "The writeback owner is not specified.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "ROOT-1",
		},
		QueueRoot: workitem.TaskPayload{
			ID:          "ROOT-1",
			Title:       "Queue split",
			Description: "Move model work into queue runners.",
			Status:      contracts.TaskStatusOpen,
		},
	})

	for _, submission := range []workitem.Submission{
		{
			Kind:           workitem.KindPreflight,
			Source:         "test-source",
			SourceRef:      "TASK-ready",
			IdempotencyKey: "test-source/TASK-ready/preflight",
			Preset:         "linux",
			Payload:        readyPayload,
			Priority:       2,
		},
		{
			Kind:           workitem.KindPreflight,
			Source:         "test-source",
			SourceRef:      "TASK-needs-info",
			IdempotencyKey: "test-source/TASK-needs-info/preflight",
			Preset:         "linux",
			Payload:        needsInfoPayload,
			Priority:       1,
		},
	} {
		if _, err := store.Submit(submission); err != nil {
			t.Fatalf("Submit(%s) error = %v", submission.SourceRef, err)
		}
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
	if err := runners.Register("runner-preflight-test", []string{"linux"}, 1); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	fakeAgent := &runnerPreflightFakeAgent{outputs: map[string]string{
		"TASK-ready":      `{"decision":"ready","confidence":0.92,"summary":"Task is actionable.","questions":[]}`,
		"TASK-needs-info": `{"decision":"needs_info","confidence":0.64,"summary":"Ownership is unclear.","questions":["Who owns the writeback?"]}`,
	}}
	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		handlers: runnerKindRegistry{
			workitem.KindPreflight: newRunnerPreflightKindHandler(func(context.Context, workitem.Item) (runnerPreflightAgent, error) {
				return runnerPreflightAgent{
					Runner: fakeAgent,
					Agent: envpreset.ResolvedAgent{
						Backend:       "fake",
						Model:         "gpt-preflight-test",
						RunnerTimeout: 3 * time.Second,
					},
					RepoRoot: "/repo/preflight",
				}, nil
			}),
		},
		environmentPresets: map[string]envpreset.Preset{
			"linux": {
				Workspace: envpreset.Workspace{
					Strategy: envpreset.WorkspaceStrategyPath,
					Path:     t.TempDir(),
				},
			},
		},
		cfg: runnerDaemonCommandConfig{
			presets:           []string{"linux"},
			runnerID:          "runner-preflight-test",
			once:              true,
			pollInterval:      time.Millisecond,
			heartbeatInterval: time.Hour,
			leaseTTL:          time.Minute,
		},
	}

	for i := 0; i < 2; i++ {
		if err := daemon.Run(context.Background()); err != nil {
			t.Fatalf("daemon Run(%d) error = %v", i+1, err)
		}
	}

	results, err := store.ListUnconsumedResults("test-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 2", len(results))
	}

	got := map[string]workitem.PreflightResult{}
	for _, queued := range results {
		if queued.Result.Status != workqueue.ResultStatusCompleted {
			t.Fatalf("result status for %s = %q, want completed", queued.Item.SourceRef, queued.Result.Status)
		}
		var payload workitem.PreflightResult
		if err := json.Unmarshal(queued.Result.Payload, &payload); err != nil {
			t.Fatalf("unmarshal result payload for %s: %v\n%s", queued.Item.SourceRef, err, queued.Result.Payload)
		}
		got[queued.Item.SourceRef] = payload
	}

	want := map[string]workitem.PreflightResult{
		"TASK-ready": {
			Verdict:    workitem.PreflightVerdictReady,
			Confidence: 0.92,
			Summary:    "Task is actionable.",
			Questions:  []string{},
		},
		"TASK-needs-info": {
			Verdict:    workitem.PreflightVerdictNeedsInfo,
			Confidence: 0.64,
			Summary:    "Ownership is unclear.",
			Questions:  []string{"Who owns the writeback?"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preflight queue results mismatch:\ngot:  %#v\nwant: %#v", got, want)
	}

	if len(fakeAgent.requests) != 2 {
		t.Fatalf("fake runner requests = %d, want 2", len(fakeAgent.requests))
	}
	for _, request := range fakeAgent.requests {
		if request.Mode != contracts.RunnerModeReview {
			t.Fatalf("runner mode = %q, want review", request.Mode)
		}
		if request.Model != "gpt-preflight-test" {
			t.Fatalf("runner model = %q, want gpt-preflight-test", request.Model)
		}
		if request.RepoRoot != "/repo/preflight" {
			t.Fatalf("runner repo root = %q, want /repo/preflight", request.RepoRoot)
		}
		if request.Timeout != 3*time.Second {
			t.Fatalf("runner timeout = %s, want 3s", request.Timeout)
		}
	}
}

func marshalRunnerPreflightPayload(t *testing.T, payload workitem.PreflightPayload) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal preflight payload: %v", err)
	}
	return raw
}

type runnerPreflightFakeAgent struct {
	outputs  map[string]string
	requests []contracts.RunnerRequest
}

func (f *runnerPreflightFakeAgent) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeRunnerOutput),
			Message: f.outputs[request.TaskID],
		})
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}
