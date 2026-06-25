package arcpr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
