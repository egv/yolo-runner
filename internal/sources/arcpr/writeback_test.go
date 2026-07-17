package arcpr

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
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceHandleResultAppliesRepliesReviewAndShipsWhenGateAllows(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcPRWritebackRuntimeState(false, []arcreview.PRCheck{{Name: "ci", Status: "passed"}}),
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		AllowShip:    true,
		State:        state,
		StateFetcher: fetcher,
		ReplyApplier: arcreview.ReplyApplier{
			Client: client,
			Store:  state,
		},
		ReviewApplier: arcreview.ReviewApplier{
			Client: client,
			Store:  state,
		},
		ShipGate: arcreview.ShipGate{
			Client: client,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Ship:     true,
		}),
	}
	replyResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			Replies: []workitem.PRReviewReply{
				{CommentID: "comment-1", Body: "The guard now covers this path."},
				{CommentID: "comment-2", Body: "Added the missing regression test."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, replyResult); err != nil {
		t.Fatalf("HandleResult(reply first) error = %v", err)
	}
	if _, err := src.HandleResult(ctx, item, replyResult); err != nil {
		t.Fatalf("HandleResult(reply retry) error = %v", err)
	}
	wantReplies := []arcPRWritebackReply{
		{prID: "42", commentID: "comment-1", body: "The guard now covers this path."},
		{prID: "42", commentID: "comment-2", body: "Added the missing regression test."},
	}
	if !reflect.DeepEqual(client.replies, wantReplies) {
		t.Fatalf("posted replies mismatch:\n got: %#v\nwant: %#v", client.replies, wantReplies)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1", "comment-2"}) {
		t.Fatalf("answered comments = %#v, want both reply IDs", answered)
	}
	if len(client.ships) != 0 {
		t.Fatalf("reply result shipped PRs, want none: %#v", client.ships)
	}

	shipResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			ReviewVerdict:    "ship",
			ShipReady:        true,
			RevisionReviewed: "r7",
		}),
	}
	fetcher.state = arcPRWritebackRuntimeState(true, []arcreview.PRCheck{{Name: "ci", Status: "pending"}})
	if _, err := src.HandleResult(ctx, item, shipResult); err != nil {
		t.Fatalf("HandleResult(ship blocked) error = %v", err)
	}
	if len(client.ships) != 0 {
		t.Fatalf("blocked ship gate shipped PRs, want none: %#v", client.ships)
	}
	if len(client.summaries) != 1 {
		t.Fatalf("review summaries = %d, want 1: %#v", len(client.summaries), client.summaries)
	}
	reviewed, err := state.GetReviewedRevision(ctx, "42")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if reviewed != "r7" {
		t.Fatalf("reviewed revision = %q, want r7", reviewed)
	}

	fetcher.state = arcPRWritebackRuntimeState(true, []arcreview.PRCheck{{Name: "ci", Status: "passed"}})
	if _, err := src.HandleResult(ctx, item, shipResult); err != nil {
		t.Fatalf("HandleResult(ship allowed) error = %v", err)
	}
	if !reflect.DeepEqual(client.ships, []string{"42"}) {
		t.Fatalf("shipped PRs = %#v, want [42]", client.ships)
	}
	if len(client.summaries) != 1 {
		t.Fatalf("retry posted duplicate review summaries: %#v", client.summaries)
	}
}

func TestSourceHandleResultDoesNotShipStaleReviewedRevision(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r8",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r8"},
			Comments: []arcreview.PRComment{
				{ID: "comment-1", Body: "Already handled.", Answered: true},
			},
			Checks: []arcreview.PRCheck{{Name: "ci", Status: "passed"}},
		},
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		AllowShip:    true,
		State:        state,
		StateFetcher: fetcher,
		ReviewApplier: arcreview.ReviewApplier{
			Client: client,
			Store:  state,
		},
		ShipGate: arcreview.ShipGate{
			Client: client,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Ship:     true,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			ReviewVerdict:    "ship",
			ShipReady:        true,
			RevisionReviewed: "r7",
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(client.ships) != 0 {
		t.Fatalf("stale reviewed revision shipped PRs, want none: %#v", client.ships)
	}
	reviewed, err := state.GetReviewedRevision(ctx, "42")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if reviewed != "r7" {
		t.Fatalf("reviewed revision = %q, want stale result revision recorded", reviewed)
	}
}

func TestSourceHandleResultFetchesWritebackStateAndShipsFromWritebackWorkspace(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "42", "r7"); err != nil {
		t.Fatalf("StoreReviewedRevision() error = %v", err)
	}

	workspace := t.TempDir()
	binDir := t.TempDir()
	writeDiscoveryFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf '%s|arc %s\n' "$PWD" "$*" >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"pr status --json 42")
  printf '%s\n' '{"id":42,"summary":"Ready PR","status":"open","from_id":"r7","from_branch":"users/alice/pr","to_branch":"trunk","checks":[{"name":"ci","status":"SUCCESS"}]}'
  ;;
"pr changes 42")
  printf '%s\n' 'diff --git a/README.md b/README.md'
  ;;
"pr merge --now 42")
  ;;
*)
  printf 'unexpected arc args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	writeDiscoveryFakeExecutable(t, binDir, "curl", `#!/bin/sh
set -eu
printf '%s|curl %s\n' "$PWD" "$*" >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/42/comments")
  printf '%s\n' '{"data":[]}'
  ;;
*)
  printf 'unexpected curl args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("ARC_SOURCE_TEST_CALLS", callsPath)
	t.Setenv("ARC_TOKEN", "test-token")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &Source{
		SourceName:         "arcpr-adapta",
		WritebackWorkspace: workspace,
		State:              state,
	}
	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Ship:     true,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			ReviewVerdict:    "ship",
			ShipReady:        true,
			RevisionReviewed: "r7",
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}

	rawCalls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("ReadFile(calls) error = %v", err)
	}
	gotCalls := strings.Split(strings.TrimSpace(string(rawCalls)), "\n")
	wantCalls := []string{
		workspace + "|arc pr status --json 42",
		workspace + "|curl -fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/42/comments",
		workspace + "|arc pr changes 42",
		workspace + "|arc pr merge --now 42",
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("writeback calls = %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestSourceHandleResultTriesConfiguredWritebackWorkspacesUntilStateFetchSucceeds(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "42", "r7"); err != nil {
		t.Fatalf("StoreReviewedRevision() error = %v", err)
	}

	firstWorkspace := t.TempDir()
	secondWorkspace := t.TempDir()
	binDir := t.TempDir()
	writeDiscoveryFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf '%s|arc %s\n' "$PWD" "$*" >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"pr status --json 42")
  if [ "$PWD" = "$ARC_SOURCE_TEST_FIRST_WORKSPACE" ]; then
    printf 'workspace cannot see PR 42\n' >&2
    exit 12
  fi
  if [ "$PWD" = "$ARC_SOURCE_TEST_SECOND_WORKSPACE" ]; then
    printf '%s\n' '{"id":42,"summary":"Ready PR","status":"open","from_id":"r7","from_branch":"users/alice/pr","to_branch":"trunk","checks":[{"name":"ci","status":"SUCCESS"}]}'
    exit 0
  fi
  printf 'unexpected workspace for status: %s\n' "$PWD" >&2
  exit 7
  ;;
"pr changes 42")
  if [ "$PWD" != "$ARC_SOURCE_TEST_SECOND_WORKSPACE" ]; then
    printf 'changes used workspace %s, want %s\n' "$PWD" "$ARC_SOURCE_TEST_SECOND_WORKSPACE" >&2
    exit 7
  fi
  printf '%s\n' 'diff --git a/README.md b/README.md'
  ;;
"pr merge --now 42")
  if [ "$PWD" != "$ARC_SOURCE_TEST_SECOND_WORKSPACE" ]; then
    printf 'merge used workspace %s, want %s\n' "$PWD" "$ARC_SOURCE_TEST_SECOND_WORKSPACE" >&2
    exit 7
  fi
  ;;
*)
  printf 'unexpected arc args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	writeDiscoveryFakeExecutable(t, binDir, "curl", `#!/bin/sh
set -eu
printf '%s|curl %s\n' "$PWD" "$*" >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/42/comments")
  if [ "$PWD" != "$ARC_SOURCE_TEST_SECOND_WORKSPACE" ]; then
    printf 'comments used workspace %s, want %s\n' "$PWD" "$ARC_SOURCE_TEST_SECOND_WORKSPACE" >&2
    exit 7
  fi
  printf '%s\n' '{"data":[]}'
  ;;
*)
  printf 'unexpected curl args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("ARC_SOURCE_TEST_CALLS", callsPath)
	t.Setenv("ARC_TOKEN", "test-token")
	t.Setenv("ARC_SOURCE_TEST_FIRST_WORKSPACE", firstWorkspace)
	t.Setenv("ARC_SOURCE_TEST_SECOND_WORKSPACE", secondWorkspace)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &Source{
		SourceName:          "arcpr-adapta",
		WritebackWorkspaces: []string{firstWorkspace, secondWorkspace},
		State:               state,
	}
	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Ship:     true,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			ReviewVerdict: "ship",
			ShipReady:     true,
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}

	rawCalls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("ReadFile(calls) error = %v", err)
	}
	gotCalls := strings.Split(strings.TrimSpace(string(rawCalls)), "\n")
	wantCalls := []string{
		firstWorkspace + "|arc pr status --json 42",
		secondWorkspace + "|arc pr status --json 42",
		secondWorkspace + "|curl -fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/42/comments",
		secondWorkspace + "|arc pr changes 42",
		secondWorkspace + "|arc pr merge --now 42",
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("writeback calls = %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestSourceHandleResultPostsAuthorArgueRepliesWithDisclosureFooter(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorArgueTestSource(t, client, "alice", true, true)
	state := src.State

	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{CommentID: "comment-1", Decision: workitem.PRReviewCommentDecisionArgue, ReplyBody: "This behavior is intentional."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}

	wantReplies := []arcPRWritebackReply{
		{prID: "42", commentID: "comment-1", body: arcreview.WithDisclosureFooter("This behavior is intentional.", "alice")},
	}
	if !reflect.DeepEqual(client.replies, wantReplies) {
		t.Fatalf("posted replies mismatch:\n got: %#v\nwant: %#v", client.replies, wantReplies)
	}

	answered, err := state.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1"}) {
		t.Fatalf("answered comments = %#v, want [comment-1]", answered)
	}

	thread, err := state.GetThreadState(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetThreadState() error = %v", err)
	}
	if thread.AnsweredAt.IsZero() {
		t.Fatalf("thread not marked answered: %#v", thread)
	}
	if thread.LastSeenReplyAt.IsZero() {
		t.Fatalf("thread watermark not recorded: %#v", thread)
	}
	if len(client.summaries) != 0 || len(client.ships) != 0 {
		t.Fatalf("author argue posted reviews/ships, want none: summaries=%#v ships=%#v", client.summaries, client.ships)
	}
}

func TestSourceHandleResultSkipsAuthorArgueRepliesWhenAutoArgueDisabled(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorArgueTestSource(t, client, "alice", true, false)
	state := src.State

	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{CommentID: "comment-1", Decision: workitem.PRReviewCommentDecisionArgue, ReplyBody: "This behavior is intentional."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(client.replies) != 0 {
		t.Fatalf("posted replies when AutoArgue disabled: %#v", client.replies)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if len(answered) != 0 {
		t.Fatalf("answered comments when AutoArgue disabled: %#v", answered)
	}
	thread, err := state.GetThreadState(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetThreadState() error = %v", err)
	}
	if !thread.AnsweredAt.IsZero() {
		t.Fatalf("thread marked answered when AutoArgue disabled: %#v", thread)
	}
}

func TestSourceHandleResultSkipsAuthorArgueRepliesWhenAuthorModeDisabled(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorArgueTestSource(t, client, "alice", false, true)
	state := src.State

	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{CommentID: "comment-1", Decision: workitem.PRReviewCommentDecisionArgue, ReplyBody: "This behavior is intentional."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(client.replies) != 0 {
		t.Fatalf("posted replies when AuthorMode disabled: %#v", client.replies)
	}
	thread, err := state.GetThreadState(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetThreadState() error = %v", err)
	}
	if !thread.AnsweredAt.IsZero() {
		t.Fatalf("thread marked answered when AuthorMode disabled: %#v", thread)
	}
}

func TestSourceHandleResultPostsAuthorResolveReplyWithFooterAndResolvesComment(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorResolveTestSource(t, client, "alice", true, true)

	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{CommentID: "comment-1", Decision: workitem.PRReviewCommentDecisionResolve, ReplyBody: "Good catch \u2014 documented in r7."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}

	wantReplies := []arcPRWritebackReply{
		{prID: "42", commentID: "comment-1", body: arcreview.WithDisclosureFooter("Good catch \u2014 documented in r7.", "alice")},
	}
	if !reflect.DeepEqual(client.replies, wantReplies) {
		t.Fatalf("posted replies mismatch:\n got: %#v\nwant: %#v", client.replies, wantReplies)
	}
	if !reflect.DeepEqual(client.resolved, []arcPRWritebackResolve{{prID: "42", commentID: "comment-1"}}) {
		t.Fatalf("resolved comments mismatch:\n got: %#v\nwant [{prID:42 commentID:comment-1}]", client.resolved)
	}
}

func TestSourceHandleResultSkipsAuthorResolveWhenResolveDisabled(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorResolveTestSource(t, client, "alice", true, false)

	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{CommentID: "comment-1", Decision: workitem.PRReviewCommentDecisionResolve, ReplyBody: "Good catch \u2014 documented in r7."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(client.replies) != 0 {
		t.Fatalf("posted replies when Resolve disabled: %#v", client.replies)
	}
	if len(client.resolved) != 0 {
		t.Fatalf("resolved comments when Resolve disabled: %#v", client.resolved)
	}
}

func TestSourceHandleResultResolvesCommentForResolvePRCommentKind(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7"},
			Comments: []arcreview.PRComment{
				{ID: "comment-1", Body: "Please resolve this once the fix lands."},
			},
		},
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		State:        state,
		StateFetcher: fetcher,
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindResolvePRComment,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.ResolvePRCommentPayload{
			PRID:      "42",
			CommentID: "comment-1",
		}),
	}
	result := workqueue.Result{Status: workqueue.ResultStatusCompleted}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if !reflect.DeepEqual(client.resolved, []arcPRWritebackResolve{{prID: "42", commentID: "comment-1"}}) {
		t.Fatalf("resolved comments mismatch:\n got: %#v\nwant [{prID:42 commentID:comment-1}]", client.resolved)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1"}) {
		t.Fatalf("answered comments = %#v, want [comment-1]", answered)
	}
	if len(client.replies) != 0 || len(client.summaries) != 0 || len(client.ships) != 0 {
		t.Fatalf("resolve-pr-comment posted replies/summaries/ships, want none: replies=%#v summaries=%#v ships=%#v", client.replies, client.summaries, client.ships)
	}
}

func TestSourceHandleResultPostsImplementationReplyThenResolvesComment(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7", Author: "alice"},
			Comments: []arcreview.PRComment{{ID: "comment-1", Body: "Please resolve this once the fix lands."}},
		},
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		State:        state,
		StateFetcher: fetcher,
		ReplyApplier: arcreview.ReplyApplier{Client: client, Store: state},
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindResolvePRComment,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.ResolvePRCommentPayload{
			PRID:      "42",
			CommentID: "comment-1",
			ReplyBody: "Fixed in `deadbeef`.",
		}),
	}

	if _, err := src.HandleResult(ctx, item, workqueue.Result{Status: workqueue.ResultStatusCompleted}); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if !reflect.DeepEqual(client.replies, []arcPRWritebackReply{{
		prID:      "42",
		commentID: "comment-1",
		body:      arcreview.WithDisclosureFooter("Fixed in `deadbeef`.", "alice"),
	}}) {
		t.Fatalf("implementation replies = %#v", client.replies)
	}
	if !reflect.DeepEqual(client.resolved, []arcPRWritebackResolve{{prID: "42", commentID: "comment-1"}}) {
		t.Fatalf("resolved comments = %#v", client.resolved)
	}
}

func TestSourceHandleResultPostsImplementationReplyForAlreadyResolvedComment(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7", Author: "alice"},
			Comments: []arcreview.PRComment{{ID: "comment-1", Body: "Please resolve this once the fix lands.", Resolved: true}},
		},
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		State:        state,
		StateFetcher: fetcher,
		ReplyApplier: arcreview.ReplyApplier{Client: client, Store: state},
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindResolvePRComment,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.ResolvePRCommentPayload{
			PRID:      "42",
			CommentID: "comment-1",
			ReplyBody: "Fixed in `deadbeef`.",
		}),
	}

	if _, err := src.HandleResult(ctx, item, workqueue.Result{Status: workqueue.ResultStatusCompleted}); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if !reflect.DeepEqual(client.replies, []arcPRWritebackReply{{
		prID:      "42",
		commentID: "comment-1",
		body:      arcreview.WithDisclosureFooter("Fixed in `deadbeef`.", "alice"),
	}}) {
		t.Fatalf("implementation replies = %#v", client.replies)
	}
	if len(client.resolved) != 0 {
		t.Fatalf("resolved an already resolved comment: %#v", client.resolved)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1"}) {
		t.Fatalf("answered comments = %#v, want [comment-1]", answered)
	}
}

func TestSourceHandleResultResolvesImplementationCommentWithoutCheckout(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	src := &Source{
		SourceName: "arcpr-adapta",
		Author:     "alice",
		State:      state,
		CommentFetcher: func(_ context.Context, prID string) ([]arcreview.PRComment, error) {
			if prID != "42" {
				t.Fatalf("comment fetch PR ID = %q, want 42", prID)
			}
			return []arcreview.PRComment{{ID: "comment-1", Body: "Please resolve this once the fix lands."}}, nil
		},
		ReplyApplier: arcreview.ReplyApplier{Client: client, Store: state},
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindResolvePRComment,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.ResolvePRCommentPayload{
			PRID:      "42",
			CommentID: "comment-1",
			ReplyBody: "Fixed in deadbeef.",
		}),
	}

	if _, err := src.HandleResult(ctx, item, workqueue.Result{Status: workqueue.ResultStatusCompleted}); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if !reflect.DeepEqual(client.replies, []arcPRWritebackReply{{
		prID:      "42",
		commentID: "comment-1",
		body:      arcreview.WithDisclosureFooter("Fixed in deadbeef.", "alice"),
	}}) {
		t.Fatalf("implementation replies = %#v", client.replies)
	}
	if !reflect.DeepEqual(client.resolved, []arcPRWritebackResolve{{prID: "42", commentID: "comment-1"}}) {
		t.Fatalf("resolved comments = %#v", client.resolved)
	}
}

func TestSourceHandleResultIgnoresUnrelatedKindsForResolvePRComment(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7"},
			Comments: []arcreview.PRComment{
				{ID: "comment-1", Body: "Please resolve this once the fix lands."},
			},
		},
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		State:        state,
		StateFetcher: fetcher,
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindImplement,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.ResolvePRCommentPayload{
			PRID:      "42",
			CommentID: "comment-1",
		}),
	}
	result := workqueue.Result{Status: workqueue.ResultStatusCompleted}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(client.resolved) != 0 {
		t.Fatalf("unrelated kind resolved comments, want none: %#v", client.resolved)
	}
}

// arcPRAuthorArgueTestSource builds a Source wired to in-memory fakes for an
// author-mode argue scenario. The fetched PR carries a single unanswered comment
// "comment-1" authored against the given PR author.
func arcPRAuthorArgueTestSource(t *testing.T, client *fakeArcPRWritebackClient, author string, authorMode, autoArgue bool) *Source {
	t.Helper()
	state := openDiscoveryTestState(t)
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7", Author: author},
			Comments: []arcreview.PRComment{
				{ID: "comment-1", Body: "This looks wrong.", Answered: false},
			},
		},
	}
	return &Source{
		SourceName:        "arcpr-adapta",
		AuthorModeEnabled: authorMode,
		AutoArgueEnabled:  autoArgue,
		State:             state,
		StateFetcher:      fetcher,
		ReplyApplier: arcreview.ReplyApplier{
			Client: client,
			Store:  state,
		},
	}
}

// arcPRAuthorResolveTestSource builds a Source wired to in-memory fakes for
// an author-mode resolve scenario. The fetched PR carries a single
// unresolved, unanswered comment "comment-1" authored against the PR author.
func arcPRAuthorResolveTestSource(t *testing.T, client *fakeArcPRWritebackClient, author string, authorMode, resolveEnabled bool) *Source {
	t.Helper()
	state := openDiscoveryTestState(t)
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7", Author: author},
			Comments: []arcreview.PRComment{
				{ID: "comment-1", Body: "Please document this option.", Answered: false},
			},
		},
	}
	return &Source{
		SourceName:        "arcpr-adapta",
		AuthorModeEnabled: authorMode,
		ResolveEnabled:    resolveEnabled,
		State:             state,
		StateFetcher:      fetcher,
		ReplyApplier: arcreview.ReplyApplier{
			Client: client,
			Store:  state,
		},
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
}

func arcPRWritebackRuntimeState(commentsAnswered bool, checks []arcreview.PRCheck) arcreview.PRRuntimeState {
	return arcreview.PRRuntimeState{
		PRID:     "42",
		Revision: "r7",
		Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7"},
		Comments: []arcreview.PRComment{
			{ID: "comment-1", Body: "Can this path return nil?", Answered: commentsAnswered},
			{ID: "comment-2", Body: "Please add coverage.", Answered: commentsAnswered},
		},
		Checks: checks,
	}
}

func mustMarshalArcPRWriteback(t *testing.T, value any) []byte {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return raw
}

type fakeArcPRWritebackStateFetcher struct {
	state arcreview.PRRuntimeState
	calls []arcPRWritebackFetchCall
}

type arcPRWritebackFetchCall struct {
	workspace string
	prID      string
}

func (f *fakeArcPRWritebackStateFetcher) FetchPRRuntimeState(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	f.calls = append(f.calls, arcPRWritebackFetchCall{workspace: workspace, prID: prID})
	return f.state, nil
}

type fakeArcPRWritebackClient struct {
	replies   []arcPRWritebackReply
	resolved  []arcPRWritebackResolve
	summaries []arcPRWritebackSummary
	ships     []string
}

type arcPRWritebackReply struct {
	prID      string
	commentID string
	body      string
}

type arcPRWritebackResolve struct {
	prID      string
	commentID string
}

type arcPRWritebackSummary struct {
	prID     string
	revision string
	body     string
}

func (c *fakeArcPRWritebackClient) PostCommentReply(_ context.Context, prID string, commentID string, body string) error {
	c.replies = append(c.replies, arcPRWritebackReply{prID: prID, commentID: commentID, body: body})
	return nil
}

func (c *fakeArcPRWritebackClient) PostReviewInlineComment(context.Context, string, string, arcreview.ReviewInlineComment) error {
	return nil
}

func (c *fakeArcPRWritebackClient) PostReviewSummary(_ context.Context, prID string, revision string, body string) error {
	c.summaries = append(c.summaries, arcPRWritebackSummary{prID: prID, revision: revision, body: body})
	return nil
}

func (c *fakeArcPRWritebackClient) Ship(_ context.Context, prID string) error {
	c.ships = append(c.ships, prID)
	return nil
}

func (c *fakeArcPRWritebackClient) ResolveComment(_ context.Context, prID string, commentID string) error {
	c.resolved = append(c.resolved, arcPRWritebackResolve{prID: prID, commentID: commentID})
	return nil
}

// arcPRAuthorImplementTestSource builds a Source wired to in-memory fakes plus a
// real work queue for an author-mode implement fan-out scenario. The fetched PR
// carries a single unresolved, unanswered comment "comment-1" and exposes the PR
// author/branch the spawned implement item must carry.
func arcPRAuthorImplementTestSource(t *testing.T, client *fakeArcPRWritebackClient, authorMode, fanOut bool) *Source {
	t.Helper()
	state := openDiscoveryTestState(t)
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcreview.PRRuntimeState{
			PRID:     "42",
			Revision: "r7",
			Details: arcreview.PRDetails{
				ID:       "42",
				Status:   "open",
				Revision: "r7",
				Author:   "alice",
				Branch:   "users/alice/pr-42",
			},
			Comments: []arcreview.PRComment{
				{ID: "comment-1", Body: "Please add a nil guard here.", Answered: false},
			},
		},
	}
	queue, err := workqueue.Open(filepath.Join(t.TempDir(), "arcpr-queue.db"))
	if err != nil {
		t.Fatalf("workqueue.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Errorf("queue.Close() error = %v", err)
		}
	})
	return &Source{
		SourceName:             "arcpr-adapta",
		Preset:                 "adapta",
		AuthorModeEnabled:      authorMode,
		ImplementFanOutEnabled: fanOut,
		State:                  state,
		StateFetcher:           fetcher,
		Queue:                  queue,
		ResolveApplier: arcreview.ResolveApplier{
			Client: client,
			Store:  state,
		},
	}
}

func TestSourceHandleResultFansOutImplementSubmissionForAuthorImplementDecision(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	state := src.State
	queue := src.Queue

	item := workitem.Item{
		ID:        "review-item-1",
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{
					CommentID: "comment-1",
					Decision:  workitem.PRReviewCommentDecisionImplement,
					Scope: &workitem.PRReviewImplementScope{
						Title:        "Add nil guard",
						Instructions: "Return early when the value is nil.",
						TargetFiles:  []string{"internal/foo/bar.go"},
					},
				},
			},
		}),
	}

	submissions, err := src.HandleResult(ctx, item, result)
	if err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: %#v", len(submissions), submissions)
	}

	got := submissions[0]
	wantKey := "arcpr/42/implement/comment-1/dbb7b294e78f" // sha256("r7")[:12]
	if got.Kind != workitem.KindImplement {
		t.Fatalf("submission kind = %q, want implement", got.Kind)
	}
	if got.Source != "arcpr-adapta" {
		t.Fatalf("submission source = %q, want arcpr-adapta", got.Source)
	}
	if got.SourceRef != "pr:42" {
		t.Fatalf("submission source ref = %q, want pr:42", got.SourceRef)
	}
	if got.IdempotencyKey != wantKey {
		t.Fatalf("submission idempotency key = %q, want %q", got.IdempotencyKey, wantKey)
	}
	if got.Preset != "adapta" {
		t.Fatalf("submission preset = %q, want adapta", got.Preset)
	}

	var payload workitem.ImplementPayload
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("unmarshal implement payload: %v", err)
	}
	if payload.Title != "Add nil guard" {
		t.Fatalf("implement title = %q, want \"Add nil guard\"", payload.Title)
	}
	if payload.Description != "Return early when the value is nil." {
		t.Fatalf("implement description = %q, want the decision scope instructions", payload.Description)
	}
	wantMeta := map[string]string{
		"arc_pr_id":       "42",
		"arc_comment_id":  "comment-1",
		"arc_comment_ids": "comment-1",
		"arc_pr_branch":   "users/alice/pr-42",
		"arc_pr_author":   "alice",
		"origin":          "arcpr-author",
	}
	if !reflect.DeepEqual(payload.PromptContext.Metadata, wantMeta) {
		t.Fatalf("implement metadata mismatch:\n got: %#v\nwant: %#v", payload.PromptContext.Metadata, wantMeta)
	}

	// The implement item was actually enqueued and is claimable.
	claimed, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("Claim() returned nil, want the spawned implement item")
	}
	if claimed.Kind != workitem.KindImplement || claimed.IdempotencyKey != wantKey {
		t.Fatalf("claimed item = kind %q key %q, want implement %q", claimed.Kind, claimed.IdempotencyKey, wantKey)
	}

	// The comment -> implement item mapping was recorded for later resolution.
	record, ok, err := state.GetCommentImplementItem(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetCommentImplementItem() error = %v", err)
	}
	if !ok {
		t.Fatalf("comment implement item mapping was not recorded")
	}
	if record.ImplementItemID != claimed.ID {
		t.Fatalf("recorded implement item id = %q, want claimed id %q", record.ImplementItemID, claimed.ID)
	}
	if record.IdempotencyKey != wantKey {
		t.Fatalf("recorded idempotency key = %q, want %q", record.IdempotencyKey, wantKey)
	}
	if record.ReviewItemID != "review-item-1" {
		t.Fatalf("recorded review item id = %q, want review-item-1", record.ReviewItemID)
	}

	// The implement decision must NOT resolve the comment here.
	if len(client.resolved) != 0 {
		t.Fatalf("implement decision resolved comment, want left open: %#v", client.resolved)
	}
}

func TestSourceHandleResultCancelsSupersededPendingImplementItem(t *testing.T) {
	ctx := context.Background()
	src := arcPRAuthorImplementTestSource(t, &fakeArcPRWritebackClient{}, true, true)
	decision := workitem.PRReviewCommentDecision{CommentID: "comment-1", Decision: workitem.PRReviewCommentDecisionImplement}
	result := workqueue.Result{
		Status:  workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{CommentDecisions: []workitem.PRReviewCommentDecision{decision}}),
	}
	firstReview := workitem.Item{
		ID:        "review-r7",
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID: "42", Revision: "r7", Mode: workitem.PRReviewModeAuthor,
		}),
	}
	if _, err := src.HandleResult(ctx, firstReview, result); err != nil {
		t.Fatalf("HandleResult(first): %v", err)
	}
	oldMapping, ok, err := src.GetCommentImplementItem(ctx, "42", "comment-1")
	if err != nil || !ok {
		t.Fatalf("GetCommentImplementItem(first) = (%#v, %t, %v), want mapping", oldMapping, ok, err)
	}

	secondReview := firstReview
	secondReview.ID = "review-r8"
	secondReview.Payload = mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
		PRID: "42", Revision: "r8", Mode: workitem.PRReviewModeAuthor,
	})
	if _, err := src.HandleResult(ctx, secondReview, result); err != nil {
		t.Fatalf("HandleResult(second): %v", err)
	}
	currentMapping, ok, err := src.GetCommentImplementItem(ctx, "42", "comment-1")
	if err != nil || !ok {
		t.Fatalf("GetCommentImplementItem(second) = (%#v, %t, %v), want mapping", currentMapping, ok, err)
	}
	if currentMapping.ImplementItemID == oldMapping.ImplementItemID {
		t.Fatalf("implement mapping was not replaced: %#v", currentMapping)
	}
	oldItem, err := src.Queue.GetItem(oldMapping.ImplementItemID)
	if err != nil {
		t.Fatalf("GetItem(old): %v", err)
	}
	if oldItem.Item.State != "cancelled" {
		t.Fatalf("old implement state = %q, want cancelled", oldItem.Item.State)
	}
	currentItem, err := src.Queue.GetItem(currentMapping.ImplementItemID)
	if err != nil {
		t.Fatalf("GetItem(current): %v", err)
	}
	if currentItem.Item.State != "pending" {
		t.Fatalf("current implement state = %q, want pending", currentItem.Item.State)
	}
}

func TestSourceHandleResultSkipsImplementFanOutWhenDisabled(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, false)
	queue := src.Queue

	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{
					CommentID: "comment-1",
					Decision:  workitem.PRReviewCommentDecisionImplement,
					Scope: &workitem.PRReviewImplementScope{
						Title:        "Add nil guard",
						Instructions: "Return early when the value is nil.",
					},
				},
			},
		}),
	}

	submissions, err := src.HandleResult(ctx, item, result)
	if err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("submissions = %#v, want none when fan-out disabled", submissions)
	}
	if claimed, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if claimed != nil {
		t.Fatalf("implement item enqueued when fan-out disabled: %#v", claimed)
	}
}
