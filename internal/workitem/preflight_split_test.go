package workitem

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestPreflightAndSplitPayloadResultConversions(t *testing.T) {
	preflightInput := preflight.RunInput{
		Task: contracts.Task{
			ID:          "ADAPTABOT-2",
			Title:       "Add queue preflight",
			Description: "Run preflight before implementation.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "ADAPTABOT-1",
			Metadata:    map[string]string{"dependencies": "ADAPTABOT-0"},
		},
		Comments: []preflight.Comment{
			{Author: "alice", Body: "Please keep the runner read-only."},
		},
		QueueRoot: contracts.Task{
			ID:          "ADAPTABOT",
			Title:       "Queue migration",
			Description: "Move model work into a durable queue.",
			Status:      contracts.TaskStatusInProgress,
		},
		Model:      "runner-owned",
		RepoRoot:   "/tmp/repo",
		Timeout:    time.Minute,
		MaxRetries: 2,
		Metadata:   map[string]string{"phase": "preflight"},
	}

	preflightPayload := PreflightPayloadFromRunInput(preflightInput)
	gotPreflightInput := preflightPayload.ToRunInput()
	assertTaskEqual(t, gotPreflightInput.Task, preflightInput.Task)
	assertTaskEqual(t, gotPreflightInput.QueueRoot, preflightInput.QueueRoot)
	if !reflect.DeepEqual(gotPreflightInput.Comments, preflightInput.Comments) {
		t.Fatalf("preflight comments = %#v, want %#v", gotPreflightInput.Comments, preflightInput.Comments)
	}
	if gotPreflightInput.Model != "" || gotPreflightInput.RepoRoot != "" || gotPreflightInput.Timeout != 0 || gotPreflightInput.MaxRetries != 0 || gotPreflightInput.Metadata != nil || gotPreflightInput.OnProgress != nil {
		t.Fatalf("preflight queue payload should not carry runner-only fields: %#v", gotPreflightInput)
	}

	preflightResult := preflight.Result{
		Decision:   preflight.DecisionReply,
		Confidence: 0.88,
		Summary:    "The newest human comment asks why preflight is needed.",
		Questions:  []string{"Which source adapter will consume this reply?"},
		ReplyText:  "Preflight prevents unclear tasks from reaching implementation.",
	}
	preflightResultPayload := PreflightResultFromResult(preflightResult)
	if preflightResultPayload.Verdict != PreflightVerdictReply {
		t.Fatalf("preflight verdict = %q, want %q", preflightResultPayload.Verdict, PreflightVerdictReply)
	}
	rawPreflightResult, err := json.Marshal(preflightResultPayload)
	if err != nil {
		t.Fatalf("marshal preflight result: %v", err)
	}
	var preflightResultJSON map[string]json.RawMessage
	if err := json.Unmarshal(rawPreflightResult, &preflightResultJSON); err != nil {
		t.Fatalf("unmarshal preflight result JSON: %v", err)
	}
	if _, ok := preflightResultJSON["verdict"]; !ok {
		t.Fatalf("preflight result JSON missing verdict: %s", rawPreflightResult)
	}
	if _, ok := preflightResultJSON["decision"]; ok {
		t.Fatalf("preflight result JSON should use verdict, not decision: %s", rawPreflightResult)
	}
	if got := preflightResultPayload.ToResult(); !reflect.DeepEqual(got, preflightResult) {
		t.Fatalf("preflight result round-trip mismatch:\ngot:  %#v\nwant: %#v", got, preflightResult)
	}

	splitInput := splitter.RunInput{
		Task: contracts.Task{
			ID:          "ADAPTABOT-3",
			Title:       "Split queue source work",
			Description: "Create source adapter tasks.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "ADAPTABOT",
			Metadata:    map[string]string{"component": "sourcehost"},
		},
		QueueRoot: contracts.Task{
			ID:          "ADAPTABOT",
			Title:       "Queue migration",
			Description: "Move model work into a durable queue.",
			Status:      contracts.TaskStatusInProgress,
		},
		Model:      "runner-owned",
		RepoRoot:   "/tmp/repo",
		Timeout:    2 * time.Minute,
		MaxRetries: 1,
		Metadata:   map[string]string{"phase": "split"},
	}

	splitPayload := SplitPayloadFromRunInput(splitInput)
	gotSplitInput := splitPayload.ToRunInput()
	assertTaskEqual(t, gotSplitInput.Task, splitInput.Task)
	assertTaskEqual(t, gotSplitInput.QueueRoot, splitInput.QueueRoot)
	if gotSplitInput.Model != "" || gotSplitInput.RepoRoot != "" || gotSplitInput.Timeout != 0 || gotSplitInput.MaxRetries != 0 || gotSplitInput.Metadata != nil || gotSplitInput.OnProgress != nil {
		t.Fatalf("split queue payload should not carry runner-only fields: %#v", gotSplitInput)
	}

	strictOutput := splitter.StrictOutput{
		Epics: []splitter.Epic{
			{Name: "Queue source adapters", Goal: "Move source-specific orchestration out of runners."},
		},
		Tasks: []splitter.Task{
			{
				ID:            "QS-S1",
				Title:         "Create source host result loop",
				Why:           []string{"Sources need idempotent result writebacks."},
				InScope:       []string{"Consume unhandled queue results.", "Seam: source host runtime."},
				OutOfScope:    []string{"Runner claim loop."},
				StrictTDD:     []string{"Add a failing source-host result-loop test first."},
				DoneWhen:      []string{"Result writebacks are marked consumed transactionally."},
				ExpectedFiles: []string{"internal/sourcehost/run.go", "internal/sourcehost/run_test.go"},
				DependsOn:     []string{"none"},
				Unlocks:       []string{"QS-S2"},
			},
			{
				ID:            "QS-S2",
				Title:         "Submit implement follow-ups",
				Why:           []string{"Ready leaf tasks should enter the queue."},
				InScope:       []string{"Create implement submissions from split children."},
				OutOfScope:    []string{"Landing policy."},
				StrictTDD:     []string{"Add a failing follow-up submission test first."},
				DoneWhen:      []string{"Follow-ups preserve dependency order."},
				ExpectedFiles: []string{"internal/sources/startrek/source.go"},
				DependsOn:     []string{"QS-S1"},
				Unlocks:       []string{"none"},
			},
		},
		Order:     []splitter.Dependency{{From: "QS-S1", To: "QS-S2"}},
		RiskNotes: []string{"Startrek writebacks must remain idempotent."},
	}
	splitResult := SplitResultFromStrictOutput(strictOutput)
	if got := splitResult.ToStrictOutput(); !reflect.DeepEqual(got, strictOutput) {
		t.Fatalf("split result round-trip mismatch:\ngot:  %#v\nwant: %#v", got, strictOutput)
	}

	rawSplitResult, err := json.Marshal(splitResult)
	if err != nil {
		t.Fatalf("marshal split result: %v", err)
	}
	var splitResultJSON map[string]json.RawMessage
	if err := json.Unmarshal(rawSplitResult, &splitResultJSON); err != nil {
		t.Fatalf("unmarshal split result JSON: %v", err)
	}
	for _, key := range []string{"epics", "tasks", "order", "risk_notes"} {
		if _, ok := splitResultJSON[key]; !ok {
			t.Fatalf("split result JSON missing %q: %s", key, rawSplitResult)
		}
	}
}

func assertTaskEqual(t *testing.T, got contracts.Task, want contracts.Task) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("task mismatch:\ngot:  %#v\nwant: %#v", got, want)
	}
}
