package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerPRReviewHandlerWritesPRReviewResultRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	arcCallsPath := installRunnerPRReviewFakeArc(t)

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

	payload, err := json.Marshal(workitem.PRReviewPayload{
		PRID:     "42",
		Revision: "r7",
		Ship:     false,
	})
	if err != nil {
		t.Fatalf("marshal PR review payload: %v", err)
	}
	submitted, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcreview",
		SourceRef:      "42:r7",
		IdempotencyKey: "arcreview/42/r7",
		Preset:         "arc",
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	claimed, err := store.Claim("runner-prreview", []string{"arc"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatal("Claim() returned nil")
	}

	fetcher := &runnerPRReviewFakeFetcher{state: arcreview.PRRuntimeState{
		PRID:     "42",
		Revision: "r7",
		Details: arcreview.PRDetails{
			ID:       "42",
			Status:   "open",
			Revision: "r7",
		},
		Comments: []arcreview.PRComment{
			{ID: "comment-1", Body: "Can this return nil?", Answered: false},
		},
		Checks: []arcreview.PRCheck{
			{Name: "ci", Status: "passed"},
		},
	}}
	model := &runnerPRReviewFakeModelHelper{payload: []byte(`{
		"summary": "Revision is ready after review.",
		"inline_comments": [],
		"replies": [
			{"comment_id": "comment-1", "body": "The nil path is covered by the guard above."}
		],
		"blockers": [],
		"ship": {"verdict": "ship", "reason": "No blockers remain."}
	}`)}

	sharedWorkspacePath := filepath.Join(t.TempDir(), "arcadia", "project")
	prMountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "42")
	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPRReview: newRunnerPRReviewKindHandler(func(_ context.Context, _ workitem.Item, workspace envpreset.Workspace, _ workitem.PRReviewPayload) (runnerPRReviewRuntime, error) {
				return runnerPRReviewRuntime{
					StateFetcher: fetcher,
					ModelHelper:  model,
					Model:        "gpt-prreview-test",
					RepoRoot:     workspace.Path,
					Timeout:      4 * time.Second,
					MaxRetries:   2,
					Metadata:     map[string]string{"reviewer": "adapta"},
				}, nil
			}),
		},
		environmentPresets: runnerDaemonTestPresets("arc"),
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{Path: sharedWorkspacePath}, nil
		},
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-prreview",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(context.Background(), *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	results, err := store.ListUnconsumedResults("arcreview")
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

	var result workitem.PRReviewResult
	if err := json.Unmarshal(got.Result.Payload, &result); err != nil {
		t.Fatalf("unmarshal PR review result payload %s: %v", got.Result.Payload, err)
	}
	want := workitem.PRReviewResult{
		Replies: []workitem.PRReviewReply{
			{CommentID: "comment-1", Body: "The nil path is covered by the guard above."},
		},
		ReviewVerdict:    "ship",
		ShipReady:        true,
		RevisionReviewed: "r7",
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("PR review result mismatch:\n got: %#v\nwant: %#v", result, want)
	}
	for _, forbidden := range []string{"status", "kind", "item_id", "summary", "inline_comments"} {
		if strings.Contains(string(got.Result.Payload), forbidden) {
			t.Fatalf("PR review result payload should not include %q: %s", forbidden, got.Result.Payload)
		}
	}

	if !reflect.DeepEqual(fetcher.calls, []runnerPRReviewFetchCall{{workspace: prMountPath, prID: "42"}}) {
		t.Fatalf("fetch calls = %#v", fetcher.calls)
	}
	if len(model.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.calls))
	}
	call := model.calls[0]
	if call.Model != "gpt-prreview-test" || call.RepoRoot != prMountPath || call.Timeout != 4*time.Second || call.MaxRetries != 2 {
		t.Fatalf("model routing fields mismatch: %#v", call)
	}
	if call.Metadata["phase"] != "pr_review" || call.Metadata["item_id"] != submitted.ID || call.Metadata["preset"] != "arc" || call.Metadata["reviewer"] != "adapta" {
		t.Fatalf("model metadata = %#v", call.Metadata)
	}

	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects")
	assertRunnerPRReviewArcCalls(t, arcCallsPath, []runnerPRReviewArcCall{
		{args: "init --repository arcadia --object-store " + objectStore + " " + prMountPath},
		{cwd: prMountPath, args: "pr checkout 42 --detached --force"},
		{cwd: prMountPath, args: "unmount --forget"},
	})
}

type runnerPRReviewFakeFetcher struct {
	state arcreview.PRRuntimeState
	calls []runnerPRReviewFetchCall
}

type runnerPRReviewFetchCall struct {
	workspace string
	prID      string
}

func (f *runnerPRReviewFakeFetcher) FetchPRRuntimeState(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	f.calls = append(f.calls, runnerPRReviewFetchCall{workspace: workspace, prID: prID})
	return f.state, nil
}

type runnerPRReviewFakeModelHelper struct {
	payload []byte
	calls   []arcPRReviewModelInput
}

func (m *runnerPRReviewFakeModelHelper) RunArcPRReviewModel(_ context.Context, input arcPRReviewModelInput) ([]byte, error) {
	m.calls = append(m.calls, input)
	return m.payload, nil
}

type runnerPRReviewArcCall struct {
	cwd  string
	args string
}

func installRunnerPRReviewFakeArc(t *testing.T) string {
	t.Helper()

	fakeBin := t.TempDir()
	callsPath := filepath.Join(t.TempDir(), "arc-calls.log")
	script := `#!/bin/sh
printf '%s\t' "$PWD" >> "$ARC_CALLS"
first=1
for arg in "$@"; do
	if [ "$first" -eq 0 ]; then
		printf ' ' >> "$ARC_CALLS"
	fi
	printf '%s' "$arg" >> "$ARC_CALLS"
	first=0
done
printf '\n' >> "$ARC_CALLS"
`
	if err := os.WriteFile(filepath.Join(fakeBin, "arc"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake arc: %v", err)
	}
	t.Setenv("ARC_CALLS", callsPath)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return callsPath
}

func assertRunnerPRReviewArcCalls(t *testing.T, path string, want []runnerPRReviewArcCall) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake arc calls: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	got := make([]runnerPRReviewArcCall, 0, len(lines))
	for _, line := range lines {
		cwd, args, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("malformed fake arc call line %q", line)
		}
		got = append(got, runnerPRReviewArcCall{cwd: cwd, args: args})
	}

	if len(got) != len(want) {
		t.Fatalf("arc calls = %#v, want %#v", got, want)
	}
	for i := range want {
		if want[i].cwd != "" && got[i].cwd != want[i].cwd {
			t.Fatalf("arc call %d cwd = %q, want %q (all calls %#v)", i, got[i].cwd, want[i].cwd, got)
		}
		if got[i].args != want[i].args {
			t.Fatalf("arc call %d args = %q, want %q (all calls %#v)", i, got[i].args, want[i].args, got)
		}
	}
}
