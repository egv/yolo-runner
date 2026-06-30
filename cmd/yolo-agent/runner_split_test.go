package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerSplitHandlerWritesSplitResultRow(t *testing.T) {
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

	payload, err := json.Marshal(workitem.SplitPayload{
		Task: workitem.TaskPayload{
			ID:          "QS-E5",
			Title:       "Add split-kind handler",
			Description: "Run strict splitting as model work only.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "QS",
		},
		QueueRoot: workitem.TaskPayload{
			ID:          "QS",
			Title:       "Queue split",
			Description: "Keep runners source-blind.",
			Status:      contracts.TaskStatusOpen,
		},
		LanguageHint: "English",
	})
	if err != nil {
		t.Fatalf("marshal split payload: %v", err)
	}

	submitted, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindSplit,
		Source:         "test-source",
		SourceRef:      "QS-E5",
		IdempotencyKey: "test-source/QS-E5/split",
		Preset:         "linux",
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	claimed, err := store.Claim("runner-test", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("Claim() returned nil")
	}

	agent := &runnerSplitFakeAgentRunner{output: runnerSplitStrictJSONOutput()}
	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindSplit: newRunnerSplitHandler(agent),
		},
		environmentPresets: runnerDaemonTestPresets("linux"),
		materialize:        runnerDaemonNoopMaterializer,
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-test",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(context.Background(), *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	results, err := store.ListUnconsumedResults("test-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	got := results[0]
	if got.Item.ID != submitted.ID {
		t.Fatalf("result item ID = %q, want %q", got.Item.ID, submitted.ID)
	}
	if got.Item.State != "done" {
		t.Fatalf("item state = %q, want done", got.Item.State)
	}
	if got.Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", got.Result.Status)
	}

	var result workitem.SplitResult
	if err := json.Unmarshal(got.Result.Payload, &result); err != nil {
		t.Fatalf("unmarshal split result payload %s: %v", got.Result.Payload, err)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("split result tasks len = %d, want 2: %#v", len(result.Tasks), result.Tasks)
	}
	if result.Tasks[0].ID != "QS-E5-A" || result.Tasks[0].Title != "Convert split payload" {
		t.Fatalf("unexpected first split task: %#v", result.Tasks[0])
	}
	if !reflect.DeepEqual(result.Order, []splitter.Dependency{{From: "QS-E5-A", To: "QS-E5-B"}}) {
		t.Fatalf("split result order = %#v", result.Order)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(got.Result.Payload, &raw); err != nil {
		t.Fatalf("unmarshal raw result payload: %v", err)
	}
	for _, forbidden := range []string{"status", "kind", "item_id", "subtasks", "created_subtasks"} {
		if _, ok := raw[forbidden]; ok {
			t.Fatalf("split result payload should not include source-side field %q: %s", forbidden, got.Result.Payload)
		}
	}

	if len(agent.requests) != 1 {
		t.Fatalf("agent requests len = %d, want 1", len(agent.requests))
	}
	request := agent.requests[0]
	if request.TaskID != "QS-E5" || request.ParentID != "QS" {
		t.Fatalf("unexpected split runner request IDs: %#v", request)
	}
	if request.Mode != contracts.RunnerModeReview {
		t.Fatalf("split runner mode = %q, want review", request.Mode)
	}
	for _, want := range []string{
		"Run the bundled strict task splitter",
		"Do not edit files, create Tracker tasks, update task status, commit, or push.",
		"ID: QS-E5",
		"Title: Add split-kind handler",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("split runner prompt missing %q:\n%s", want, request.Prompt)
		}
	}
}

func TestRunnerSplitKindHandlerUsesResolvedPresetAgent(t *testing.T) {
	payload, err := json.Marshal(workitem.SplitPayload{
		Task: workitem.TaskPayload{
			ID:          "LUMI-27",
			Title:       "Выдача ачивки за прохождение экскурсии",
			Description: "Split Startrek parent work into implementable tasks.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "LUMI",
		},
		QueueRoot: workitem.TaskPayload{
			ID:     "LUMI",
			Title:  "LUMI",
			Status: contracts.TaskStatusOpen,
		},
	})
	if err != nil {
		t.Fatalf("marshal split payload: %v", err)
	}

	fakeAgent := &runnerSplitFakeAgentRunner{output: runnerSplitStrictJSONOutput()}
	handler := newRunnerSplitKindHandler(func(context.Context, workitem.Item) (runnerSplitAgent, error) {
		return runnerSplitAgent{
			Runner: fakeAgent,
			Agent: envpreset.ResolvedAgent{
				Backend:       "codex",
				Model:         "gpt-test",
				RunnerTimeout: 2 * time.Minute,
			},
			RepoRoot: "/repo/root",
		}, nil
	})

	item := workitem.Item{
		ID:             "split-item",
		Kind:           workitem.KindSplit,
		Source:         "startrek-startrek-adapta",
		SourceRef:      "LUMI-27",
		IdempotencyKey: "st/LUMI-27/split/rev",
		Preset:         "startrek-lumi",
		Payload:        payload,
	}
	result, err := handler(context.Background(), item, envpreset.Workspace{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	var splitResult workitem.SplitResult
	if err := json.Unmarshal(result.Payload, &splitResult); err != nil {
		t.Fatalf("unmarshal split result %s: %v", result.Payload, err)
	}
	if len(splitResult.Tasks) != 2 {
		t.Fatalf("split result tasks len = %d, want 2", len(splitResult.Tasks))
	}
	if len(fakeAgent.requests) != 1 {
		t.Fatalf("agent requests len = %d, want 1", len(fakeAgent.requests))
	}
	request := fakeAgent.requests[0]
	if request.Model != "gpt-test" {
		t.Fatalf("request model = %q, want gpt-test", request.Model)
	}
	if request.RepoRoot != "/repo/root" {
		t.Fatalf("request repo root = %q, want /repo/root", request.RepoRoot)
	}
	if request.Timeout != 2*time.Minute {
		t.Fatalf("request timeout = %s, want 2m", request.Timeout)
	}
	if request.Metadata["phase"] != "split" || request.Metadata["preset"] != "startrek-lumi" || request.Metadata["source_ref"] != "LUMI-27" {
		t.Fatalf("unexpected request metadata: %#v", request.Metadata)
	}
}

type runnerSplitFakeAgentRunner struct {
	output   string
	requests []contracts.RunnerRequest
}

func (f *runnerSplitFakeAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeAgentText),
			Message: f.output,
		})
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func runnerSplitStrictJSONOutput() string {
	return `{
		"epics": [
			{"name": "Split runner", "goal": "Return strict split output from runner-owned model work."}
		],
		"tasks": [
			{
				"id": "QS-E5-A",
				"title": "Convert split payload",
				"why": ["The runner needs typed splitter input."],
				"in_scope": ["Decode the split payload.", "Call the strict splitter runner."],
				"out_of_scope": ["Creating tracker subtasks."],
				"strict_tdd": ["Add a failing split handler test first."],
				"done_when": ["The split handler emits a typed result."],
				"expected_files": ["cmd/yolo-agent/runner_split.go", "cmd/yolo-agent/runner_split_test.go"],
				"depends_on": ["none"],
				"unlocks": ["QS-E5-B"]
			},
			{
				"id": "QS-E5-B",
				"title": "Persist split result",
				"why": ["Sources need the strict output to create follow-up work."],
				"in_scope": ["Marshal the strict output as a split result."],
				"out_of_scope": ["Source-specific writebacks."],
				"strict_tdd": ["Re-run the split handler test."],
				"done_when": ["The queue contains a split result row."],
				"expected_files": ["cmd/yolo-agent/runner_split.go", "cmd/yolo-agent/runner_split_test.go"],
				"depends_on": ["QS-E5-A"],
				"unlocks": ["none"]
			}
		],
		"order": [
			{"from": "QS-E5-A", "to": "QS-E5-B"}
		],
		"risk_notes": ["none"]
	}`
}
