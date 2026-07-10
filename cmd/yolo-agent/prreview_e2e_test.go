package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	"github.com/egv/yolo-runner/v2/internal/sources/arcpr"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestPRReviewEndToEndOffline(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)

	binDir := t.TempDir()
	arcCallsPath := filepath.Join(t.TempDir(), "arc-calls.log")
	installPRReviewE2EFakeArc(t, binDir)
	t.Setenv("PRREVIEW_E2E_ARC_CALLS", arcCallsPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	queuePath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close(queue) error = %v", err)
		}
	})

	stateStore, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "arcpr-state.db"))
	if err != nil {
		t.Fatalf("Open(state) error = %v", err)
	}
	t.Cleanup(func() {
		if err := stateStore.Close(); err != nil {
			t.Errorf("Close(state) error = %v", err)
		}
	})

	writebackClient := &prReviewE2EFakeArcanumClient{}
	writebackFetcher := &prReviewE2EWritebackFetcher{
		state: prReviewE2ERuntimeState(),
	}
	source := &arcpr.Source{
		SourceName:   "arcpr-arc-dev",
		Preset:       "arc-dev",
		State:        stateStore,
		Lister:       prReviewE2EIncomingLister{},
		StateFetcher: writebackFetcher,
		ReplyApplier: arcreview.ReplyApplier{
			Client: writebackClient,
			Store:  stateStore,
		},
		ReviewApplier: arcreview.ReviewApplier{
			Client: writebackClient,
			Store:  stateStore,
		},
		ShipGate: arcreview.ShipGate{
			Client: writebackClient,
		},
	}

	if err := sourcehost.Run(ctx, source, store, sourcehost.Options{Once: true, ProcID: "arcpr-source"}); err != nil {
		t.Fatalf("sourcehost Run(discover) error = %v", err)
	}

	claimed, err := store.Claim("prreview-runner", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatal("expected queued PR review item")
	}
	if claimed.Kind != workitem.KindPRReview || claimed.Source != "arcpr-arc-dev" || claimed.SourceRef != "pr:777" {
		t.Fatalf("claimed item = %#v, want arcpr pr-review item for PR 777", claimed)
	}

	payload, err := workitem.DecodePRReviewPayload(claimed.Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if payload.PRID != "777" || payload.Revision != "rev-777" || payload.Ship {
		t.Fatalf("payload = %#v, want PR 777 rev-777 with no ship", payload)
	}

	modelRunner := &fakeArcPRReviewModelRunner{payload: []byte(`{
		"summary": "The revision needs a follow-up before shipping.",
		"inline_comments": [],
		"replies": [
			{"comment_id": "comment-1", "body": "The review path now exercises the minion project context."}
		],
		"blockers": [],
		"ship": {"verdict": "do_not_ship", "reason": "Offline gate must not ship."}
	}`)}
	runnerFetcher := &runnerPRReviewFakeFetcher{state: prReviewE2ERuntimeState()}
	daemon := runnerDaemon{
		store: store,
		handlers: runnerKindRegistry{
			workitem.KindPRReview: newRunnerPRReviewKindHandler(func(_ context.Context, _ workitem.Item, workspace envpreset.Workspace, _ workitem.PRReviewPayload) (runnerPRReviewRuntime, error) {
				return runnerPRReviewRuntime{
					StateFetcher: runnerFetcher,
					ModelHelper: arcPRReviewCycleModelHelperFunc(func(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
						return runArcPRReviewModel(ctx, modelRunner, input)
					}),
					Model:      "gpt-prreview-e2e",
					RepoRoot:   workspace.Path,
					Timeout:    5 * time.Second,
					MaxRetries: 1,
				}, nil
			}),
		},
		environmentPresets: runnerDaemonTestPresets("arc-dev"),
		cfg: runnerDaemonCommandConfig{
			runnerID:          "prreview-runner",
			heartbeatInterval: time.Hour,
		},
	}
	if err := daemon.runClaimedItem(ctx, *claimed); err != nil {
		t.Fatalf("runClaimedItem() error = %v", err)
	}

	prMountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "777")
	if !reflect.DeepEqual(runnerFetcher.calls, []runnerPRReviewFetchCall{{workspace: prMountPath, prID: "777"}}) {
		t.Fatalf("runner fetch calls = %#v, want PR checkout mount", runnerFetcher.calls)
	}
	if len(modelRunner.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(modelRunner.requests))
	}
	prompt := modelRunner.requests[0].Prompt
	for _, want := range []string{
		"Project context:",
		"Root: taxi/backend-cpp/services/ai_minion",
		"Build/test command: ya make -t taxi/backend-cpp/services/ai_minion",
		"AGENTS.md:",
		"Use service-specific AI minion review conventions.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	if err := sourcehost.Run(ctx, source, store, sourcehost.Options{Once: true, ProcID: "arcpr-source"}); err != nil {
		t.Fatalf("sourcehost Run(writeback) error = %v", err)
	}

	if _, err := os.Stat(prMountPath); !os.IsNotExist(err) {
		t.Fatalf("PR mount path still exists after cleanup: err=%v path=%s", err, prMountPath)
	}
	testCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	assertPRReviewE2EArcCalls(t, arcCallsPath, []string{
		testCWD + "\tarc mount -m " + prMountPath + " -S " + filepath.Join(home, ".yolo-runner", "pr-objects", "777"),
		prMountPath + "\tarc pr checkout 777 --detached --force",
		testCWD + "\tarc unmount --force --forget " + prMountPath,
	})

	if !reflect.DeepEqual(writebackClient.replies, []prReviewE2EReply{{
		prID:      "777",
		commentID: "comment-1",
		body:      "The review path now exercises the minion project context.",
	}}) {
		t.Fatalf("writeback replies = %#v", writebackClient.replies)
	}
	wantSummaryBody := strings.Join([]string{
		"The revision needs a follow-up before shipping.",
		"",
		"Offline gate must not ship.",
		"",
		"По результатам ревью — не к мержу, есть открытые замечания.",
		"",
		"<!-- yolo-reviewer: reviewed_from_id=rev-777 -->",
	}, "\n")
	if !reflect.DeepEqual(writebackClient.summaries, []prReviewE2ESummary{{
		prID:     "777",
		revision: "rev-777",
		body:     wantSummaryBody,
	}}) {
		t.Fatalf("writeback review summaries = %#v", writebackClient.summaries)
	}
	if len(writebackClient.ships) != 0 {
		t.Fatalf("offline PR review shipped PRs, want none: %#v", writebackClient.ships)
	}

	reviewed, err := stateStore.GetReviewedRevision(ctx, "777")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if reviewed != "rev-777" {
		t.Fatalf("reviewed revision = %q, want rev-777", reviewed)
	}
	answered, err := stateStore.ListAnsweredCommentIDs(ctx, "777")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1"}) {
		t.Fatalf("answered comment IDs = %#v, want [comment-1]", answered)
	}
	unconsumed, err := store.ListUnconsumedResults("arcpr-arc-dev")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(unconsumed) != 0 {
		t.Fatalf("unconsumed results = %d, want 0", len(unconsumed))
	}
	next, err := store.Claim("prreview-runner-2", []string{"arc-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(after writeback) error = %v", err)
	}
	if next != nil {
		t.Fatalf("expected no duplicate queued item after writeback, got %#v", next)
	}
}

type prReviewE2EIncomingLister struct{}

func (prReviewE2EIncomingLister) ListReviewPRs(context.Context) ([]arcanum.PRSummary, error) {
	return []arcanum.PRSummary{
		{ID: "777", FromID: "rev-777", Status: "open"},
	}, nil
}

type prReviewE2EWritebackFetcher struct {
	state arcreview.PRRuntimeState
	calls []runnerPRReviewFetchCall
}

func (f *prReviewE2EWritebackFetcher) FetchPRRuntimeState(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	f.calls = append(f.calls, runnerPRReviewFetchCall{workspace: workspace, prID: prID})
	return f.state, nil
}

type prReviewE2EFakeArcanumClient struct {
	replies   []prReviewE2EReply
	summaries []prReviewE2ESummary
	ships     []string
}

type prReviewE2EReply struct {
	prID      string
	commentID string
	body      string
}

type prReviewE2ESummary struct {
	prID     string
	revision string
	body     string
}

func (c *prReviewE2EFakeArcanumClient) PostCommentReply(_ context.Context, prID string, commentID string, body string) error {
	c.replies = append(c.replies, prReviewE2EReply{prID: prID, commentID: commentID, body: body})
	return nil
}

func (c *prReviewE2EFakeArcanumClient) PostReviewInlineComment(context.Context, string, string, arcreview.ReviewInlineComment) error {
	return nil
}

func (c *prReviewE2EFakeArcanumClient) PostReviewSummary(_ context.Context, prID string, revision string, body string) error {
	c.summaries = append(c.summaries, prReviewE2ESummary{prID: prID, revision: revision, body: body})
	return nil
}

func (c *prReviewE2EFakeArcanumClient) Ship(_ context.Context, prID string) error {
	c.ships = append(c.ships, prID)
	return nil
}

func prReviewE2ERuntimeState() arcreview.PRRuntimeState {
	return arcreview.PRRuntimeState{
		PRID:     "777",
		Revision: "rev-777",
		Details: arcreview.PRDetails{
			ID:       "777",
			Title:    "Review AI minion retry handling",
			Status:   "open",
			Revision: "rev-777",
		},
		Comments: []arcreview.PRComment{
			{ID: "comment-1", Body: "Please confirm the minion review path uses project context."},
		},
		Checks: []arcreview.PRCheck{
			{Name: "ci", Status: "passed"},
		},
		ChangedFiles: []arcreview.PRChangedFile{
			{
				Path:   "taxi/backend-cpp/services/ai_minion/main.cpp",
				Status: "modified",
				Diff:   "@@ -1 +1 @@\n-int old_retry;\n+int deterministic_retry;",
			},
		},
	}
}

func installPRReviewE2EFakeArc(t *testing.T, dir string) {
	t.Helper()

	writeSourceArcPRFakeExecutable(t, dir, "arc", `#!/bin/sh
set -eu
printf '%s	arc' "$PWD" >> "$PRREVIEW_E2E_ARC_CALLS"
for arg in "$@"; do
  printf ' %s' "$arg" >> "$PRREVIEW_E2E_ARC_CALLS"
done
printf '\n' >> "$PRREVIEW_E2E_ARC_CALLS"
case "$*" in
"mount -m "*)
  mkdir -p "$3"
  ;;
"pr checkout 777 --detached --force")
  mkdir -p taxi/backend-cpp/services/ai_minion
  printf '# fixture ya.make\n' > taxi/backend-cpp/services/ai_minion/ya.make
  printf 'Use service-specific AI minion review conventions.\n' > taxi/backend-cpp/services/ai_minion/AGENTS.md
  printf 'int deterministic_retry;\n' > taxi/backend-cpp/services/ai_minion/main.cpp
  ;;
"unmount --force --forget "*)
  ;;
*)
  printf 'unexpected arc args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
}

func assertPRReviewE2EArcCalls(t *testing.T, path string, want []string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(arc calls) error = %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arc calls = %#v, want %#v", got, want)
	}
}
