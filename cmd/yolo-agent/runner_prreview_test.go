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
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
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
		ChangedFiles: []arcreview.PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
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
		Summary: "Revision is ready after review.",
		Replies: []workitem.PRReviewReply{
			{CommentID: "comment-1", Body: "The nil path is covered by the guard above."},
		},
		ReviewVerdict:    "ship",
		ShipReason:       "No blockers remain.",
		ShipReady:        true,
		RevisionReviewed: "r7",
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("PR review result mismatch:\n got: %#v\nwant: %#v", result, want)
	}
	for _, forbidden := range []string{"status", "kind", "item_id"} {
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

	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "42")
	assertRunnerPRReviewArcCalls(t, arcCallsPath, []runnerPRReviewArcCall{
		{args: "mount -m " + prMountPath + " -S " + objectStore},
		{cwd: prMountPath, args: "pr checkout 42 --detached --force"},
		{args: "unmount --force --forget " + prMountPath},
	})
}

func TestRunnerPRReviewHandlerPassesProjectContextIntoPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installRunnerPRReviewFakeArc(t)

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
	})
	if err != nil {
		t.Fatalf("marshal PR review payload: %v", err)
	}
	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcreview",
		SourceRef:      "42:r7",
		IdempotencyKey: "arcreview/42/r7",
		Preset:         "arc",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	claimed, err := store.Claim("runner-prreview", []string{"arc"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatal("Claim() returned nil")
	}

	state := arcreview.PRRuntimeState{
		PRID:     "42",
		Revision: "r7",
		Details: arcreview.PRDetails{
			ID:       "42",
			Status:   "open",
			Revision: "r7",
			Issues: []arcreview.PRIssue{
				{ID: "TAXI-42", Status: "open", Message: "Keep AI minion retries deterministic"},
			},
		},
		OpenIssues: []arcreview.PRIssue{
			{ID: "TAXI-42", Status: "open", Message: "Keep AI minion retries deterministic"},
		},
		ChangedFiles: []arcreview.PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		},
		Checks: []arcreview.PRCheck{
			{Name: "ci", Status: "passed"},
		},
	}
	fetcher := &runnerPRReviewFakeFetcher{state: state}
	runner := &fakeArcPRReviewModelRunner{payload: []byte(`{
		"summary": "Revision is ready after context-aware review.",
		"inline_comments": [],
		"replies": [],
		"blockers": [],
		"ship": {"verdict": "ship", "reason": "No blockers remain."}
	}`)}
	tracker := &runnerPRReviewFakeLinkedTicketTracker{
		issues: map[string]trackerstartrek.Issue{
			"TAXI-42": {
				ID:     "TAXI-42",
				Title:  "Keep AI minion retries deterministic",
				Status: "open",
				Description: strings.Join([]string{
					"Intent:",
					"Reviewers need retry behavior checked against the ticket.",
					"",
					"Acceptance Criteria:",
					"- retries preserve ordering",
					"- tests cover retry ordering",
				}, "\n"),
			},
		},
	}

	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPRReview: newRunnerPRReviewKindHandler(func(_ context.Context, _ workitem.Item, workspace envpreset.Workspace, _ workitem.PRReviewPayload) (runnerPRReviewRuntime, error) {
				return runnerPRReviewRuntime{
					StateFetcher: fetcher,
					ModelHelper: arcPRReviewCycleModelHelperFunc(func(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
						return runArcPRReviewModel(ctx, runner, input)
					}),
					LinkedTicketTracker: tracker,
					Model:               "gpt-prreview-test",
					RepoRoot:            workspace.Path,
					Timeout:             4 * time.Second,
				}, nil
			}),
		},
		environmentPresets: runnerDaemonTestPresets("arc"),
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{Path: filepath.Join(t.TempDir(), "unused-shared-arcadia")}, nil
		},
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-prreview",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(context.Background(), *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	prompt := runner.requests[0].Prompt
	for _, want := range []string{
		"Project context:",
		"Root: taxi/backend-cpp/services/ai_minion",
		"Build/test command: ya make -t taxi/backend-cpp/services/ai_minion",
		"Conventions excerpt:",
		"AGENTS.md:",
		"Use service-specific AI minion review conventions.",
		"Linked ticket acceptance criteria:",
		"TAXI-42 - Keep AI minion retries deterministic:",
		"- retries preserve ordering",
		"- tests cover retry ordering",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	if !reflect.DeepEqual(tracker.calls, []string{"TAXI-42"}) {
		t.Fatalf("linked ticket tracker calls = %#v, want TAXI-42", tracker.calls)
	}
}

func TestRunnerPRReviewSkipsPresetArcSharedMaterialization(t *testing.T) {
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
	})
	if err != nil {
		t.Fatalf("marshal PR review payload: %v", err)
	}
	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcreview",
		SourceRef:      "42:r7",
		IdempotencyKey: "arcreview/42/r7",
		Preset:         "arc",
		Payload:        payload,
	}); err != nil {
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
		Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7"},
		ChangedFiles: []arcreview.PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		},
	}}
	model := &runnerPRReviewFakeModelHelper{payload: []byte(`{
		"summary": "Revision reviewed.",
		"inline_comments": [],
		"replies": [],
		"blockers": [],
		"ship": {"verdict": "hold", "reason": "Needs follow-up."}
	}`)}

	sharedMount := filepath.Join(t.TempDir(), "shared-arc")
	sharedSubpath := "project"
	if err := os.MkdirAll(filepath.Join(sharedMount, sharedSubpath), 0o755); err != nil {
		t.Fatalf("create shared arc workspace: %v", err)
	}

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
				}, nil
			}),
		},
		environmentPresets: map[string]envpreset.Preset{
			"arc": {
				Workspace: envpreset.Workspace{
					Strategy: envpreset.WorkspaceStrategyArcShared,
					Mount:    sharedMount,
					Subpath:  sharedSubpath,
				},
			},
		},
		materialize: envpreset.MaterializeWorkspace,
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-prreview",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(context.Background(), *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	prMountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "42")
	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "42")
	assertRunnerPRReviewArcCalls(t, arcCallsPath, []runnerPRReviewArcCall{
		{args: "mount -m " + prMountPath + " -S " + objectStore},
		{cwd: prMountPath, args: "pr checkout 42 --detached --force"},
		{args: "unmount --force --forget " + prMountPath},
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

type runnerPRReviewFakeLinkedTicketTracker struct {
	issues map[string]trackerstartrek.Issue
	calls  []string
}

func (f *runnerPRReviewFakeLinkedTicketTracker) GetIssue(_ context.Context, issueID string) (trackerstartrek.Issue, error) {
	f.calls = append(f.calls, issueID)
	return f.issues[issueID], nil
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
if [ "$1" = "mount" ] && [ "$2" = "-l" ]; then
	printf '[]\n'
fi
if [ "$1" = "pr" ] && [ "$2" = "checkout" ]; then
	mkdir -p taxi/backend-cpp/services/ai_minion
	printf '# fixture ya.make\n' > taxi/backend-cpp/services/ai_minion/ya.make
	printf 'Use service-specific AI minion review conventions.\n' > taxi/backend-cpp/services/ai_minion/AGENTS.md
fi
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

func TestRunnerPRReviewHandlerAuthorModeBuildsAuthorPromptAndCapturesDecisions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installRunnerPRReviewFakeArc(t)

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
		Revision: "34091604",
		Mode:     workitem.PRReviewModeAuthor,
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
		Revision: "19076b3f8b9c21a4bac71383698ddb880667c96a",
		Details: arcreview.PRDetails{
			ID:       "42",
			Status:   "open",
			Revision: "19076b3f8b9c21a4bac71383698ddb880667c96a",
		},
		Comments: []arcreview.PRComment{
			{ID: "comment-1", Body: "Can this return nil?", Answered: false},
		},
		ChangedFiles: []arcreview.PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		},
	}}
	runner := &fakeArcPRReviewModelRunner{payload: []byte(`Проверил комментарии.
` + "```json" + `
{
		"comment_decisions": [
			{
				"comment_id": "comment-1",
				"decision": "resolve",
				"language": "en",
				"reply_body": "The nil path is guarded above.",
				"rationale": "Question answered by the guard."
			}
		]
	}
` + "```" + `
Готово.`)}

	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPRReview: newRunnerPRReviewKindHandler(func(_ context.Context, _ workitem.Item, workspace envpreset.Workspace, _ workitem.PRReviewPayload) (runnerPRReviewRuntime, error) {
				return runnerPRReviewRuntime{
					StateFetcher: fetcher,
					ModelHelper: arcPRReviewCycleModelHelperFunc(func(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
						return runArcPRReviewModel(ctx, runner, input)
					}),
					Model:    "gpt-prreview-test",
					RepoRoot: workspace.Path,
					Timeout:  4 * time.Second,
				}, nil
			}),
		},
		environmentPresets: runnerDaemonTestPresets("arc"),
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{Path: filepath.Join(t.TempDir(), "unused-shared-arcadia")}, nil
		},
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-prreview",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(context.Background(), *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	// Author mode builds the author prompt, not the reviewer prompt.
	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	prompt := runner.requests[0].Prompt
	if !strings.Contains(prompt, "Action: author_review") {
		t.Fatalf("expected author-mode prompt to contain %q, got:\n%s", "Action: author_review", prompt)
	}
	if strings.Contains(prompt, "Action: review_revision") {
		t.Fatalf("author-mode prompt must not contain %q, got:\n%s", "Action: review_revision", prompt)
	}

	// Author mode captures per-comment decisions into CommentDecisions.
	results, err := store.ListUnconsumedResults("arcreview")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	if results[0].Item.ID != submitted.ID {
		t.Fatalf("result item ID = %q, want %q", results[0].Item.ID, submitted.ID)
	}

	var result workitem.PRReviewResult
	if err := json.Unmarshal(results[0].Result.Payload, &result); err != nil {
		t.Fatalf("unmarshal PR review result payload %s: %v", results[0].Result.Payload, err)
	}
	want := workitem.PRReviewResult{
		CommentDecisions: []workitem.PRReviewCommentDecision{
			{
				CommentID: "comment-1",
				Decision:  workitem.PRReviewCommentDecisionResolve,
				Language:  "en",
				ReplyBody: "The nil path is guarded above.",
				Rationale: "Question answered by the guard.",
			},
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("PR review result mismatch:\n got: %#v\nwant: %#v", result, want)
	}
}

func TestRunnerPRReviewHandlerReviewerAnswerModeIsUnchanged(t *testing.T) {
	// The default reviewer mode on the answer path must keep building the
	// reviewer prompt and capturing replies (not author comment decisions).
	home := t.TempDir()
	t.Setenv("HOME", home)
	installRunnerPRReviewFakeArc(t)

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
		PRID:                 "42",
		Revision:             "r7",
		UnansweredCommentIDs: []string{"comment-1"},
	})
	if err != nil {
		t.Fatalf("marshal PR review payload: %v", err)
	}
	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcreview",
		SourceRef:      "42:r7",
		IdempotencyKey: "arcreview/42/r7/reviewer-answer",
		Preset:         "arc",
		Payload:        payload,
	}); err != nil {
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
		ChangedFiles: []arcreview.PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		},
	}}
	runner := &fakeArcPRReviewModelRunner{payload: []byte(`{
		"replies": [
			{"comment_id": "comment-1", "body": "The nil path is guarded above."}
		]
	}`)}

	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPRReview: newRunnerPRReviewKindHandler(func(_ context.Context, _ workitem.Item, workspace envpreset.Workspace, _ workitem.PRReviewPayload) (runnerPRReviewRuntime, error) {
				return runnerPRReviewRuntime{
					StateFetcher: fetcher,
					ModelHelper: arcPRReviewCycleModelHelperFunc(func(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
						return runArcPRReviewModel(ctx, runner, input)
					}),
					Model:    "gpt-prreview-test",
					RepoRoot: workspace.Path,
					Timeout:  4 * time.Second,
				}, nil
			}),
		},
		environmentPresets: runnerDaemonTestPresets("arc"),
		materialize: func(context.Context, envpreset.Preset, string, bool) (envpreset.Workspace, error) {
			return envpreset.Workspace{Path: filepath.Join(t.TempDir(), "unused-shared-arcadia")}, nil
		},
		cfg: runnerDaemonCommandConfig{
			runnerID:          "runner-prreview",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(context.Background(), *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	if len(runner.requests) != 1 {
		t.Fatalf("runner requests = %d, want 1", len(runner.requests))
	}
	prompt := runner.requests[0].Prompt
	if !strings.Contains(prompt, "Action: review_revision") {
		t.Fatalf("expected reviewer prompt to contain %q, got:\n%s", "Action: review_revision", prompt)
	}
	if strings.Contains(prompt, "Action: author_review") {
		t.Fatalf("reviewer prompt must not contain %q, got:\n%s", "Action: author_review", prompt)
	}

	results, err := store.ListUnconsumedResults("arcreview")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	var result workitem.PRReviewResult
	if err := json.Unmarshal(results[0].Result.Payload, &result); err != nil {
		t.Fatalf("unmarshal PR review result payload %s: %v", results[0].Result.Payload, err)
	}
	want := workitem.PRReviewResult{
		Replies: []workitem.PRReviewReply{
			{CommentID: "comment-1", Body: "The nil path is guarded above."},
		},
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("PR review result mismatch:\n got: %#v\nwant: %#v", result, want)
	}
}
