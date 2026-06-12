package startrek

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceHandlePreflightResultWritesNeedsInfoReplyAndRecordsState(t *testing.T) {
	ctx := context.Background()

	t.Run("needs info", func(t *testing.T) {
		state, err := OpenState(filepath.Join(t.TempDir(), "source.db"))
		if err != nil {
			t.Fatalf("OpenState() error = %v", err)
		}
		t.Cleanup(func() {
			if err := state.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		tracker := &fakePreflightWritebackTracker{}
		src := Source{
			Tracker:         tracker,
			State:           state,
			ProcessingLabel: "yolo-agent-in-progress",
		}

		item := preflightWritebackItem(t, "item-needs-info", "VAY-42", "st/VAY-42/preflight/rev1", contracts.Task{
			ID:          "VAY-42",
			Title:       "Clarify ownership",
			Description: "Author: Ada Lovelace (author-1)",
		})
		result := preflightWritebackResult(t, item.ID, workitem.PreflightResult{
			Verdict:   workitem.PreflightVerdictNeedsInfo,
			Summary:   "Ownership is unclear.",
			Questions: []string{"Which package owns this behavior?"},
		})

		followUps, err := src.HandleResult(ctx, item, result)
		if err != nil {
			t.Fatalf("HandleResult(needs_info) error = %v", err)
		}
		if len(followUps) != 0 {
			t.Fatalf("HandleResult(needs_info) follow-ups = %#v, want none", followUps)
		}

		wantOps := []string{
			"comments VAY-42",
			"remove VAY-42 yolo-agent-in-progress",
			"add VAY-42 needs-info",
			"comment VAY-42 marker=needs-info author=author-1",
			"data VAY-42 needs-info comment-1",
		}
		if !reflect.DeepEqual(tracker.ops, wantOps) {
			t.Fatalf("needs-info backend ops mismatch:\n got: %#v\nwant: %#v", tracker.ops, wantOps)
		}
		if len(tracker.comments) != 1 {
			t.Fatalf("needs-info comments = %d, want 1", len(tracker.comments))
		}
		for _, want := range []string{
			"<!-- yolo-runner:needs-info -->",
			"Ownership is unclear.",
			"Which package owns this behavior?",
		} {
			if !strings.Contains(tracker.comments[0].Body, want) {
				t.Fatalf("needs-info comment missing %q:\n%s", want, tracker.comments[0].Body)
			}
		}

		record, ok, err := state.GetPreflightWriteback(ctx, item.IdempotencyKey)
		if err != nil {
			t.Fatalf("GetPreflightWriteback() error = %v", err)
		}
		if !ok {
			t.Fatalf("expected preflight writeback state for %q", item.IdempotencyKey)
		}
		if record.IdempotencyKey != item.IdempotencyKey || record.IssueID != "VAY-42" || record.Verdict != workitem.PreflightVerdictNeedsInfo || record.CommentID != "comment-1" {
			t.Fatalf("unexpected preflight writeback record: %#v", record)
		}

		if _, err := src.HandleResult(ctx, item, result); err != nil {
			t.Fatalf("HandleResult(needs_info duplicate) error = %v", err)
		}
		if !reflect.DeepEqual(tracker.ops, wantOps) {
			t.Fatalf("duplicate needs-info result should not call backend again:\n got: %#v\nwant: %#v", tracker.ops, wantOps)
		}
	})

	t.Run("reply", func(t *testing.T) {
		state, err := OpenState(filepath.Join(t.TempDir(), "source.db"))
		if err != nil {
			t.Fatalf("OpenState() error = %v", err)
		}
		t.Cleanup(func() {
			if err := state.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		tracker := &fakePreflightWritebackTracker{}
		src := Source{
			Tracker:         tracker,
			State:           state,
			ProcessingLabel: "yolo-agent-in-progress",
		}

		item := preflightWritebackItem(t, "item-reply", "VAY-43", "st/VAY-43/preflight/rev2", contracts.Task{
			ID:          "VAY-43",
			Title:       "Answer follow-up",
			Description: "Author: Grace Hopper (author-2)",
		})
		result := preflightWritebackResult(t, item.ID, workitem.PreflightResult{
			Verdict:   workitem.PreflightVerdictReply,
			ReplyText: "Simple answer: the package owner is adapta/messenger.",
		})

		followUps, err := src.HandleResult(ctx, item, result)
		if err != nil {
			t.Fatalf("HandleResult(reply) error = %v", err)
		}
		if len(followUps) != 0 {
			t.Fatalf("HandleResult(reply) follow-ups = %#v, want none", followUps)
		}

		wantOps := []string{
			"remove VAY-43 yolo-agent-in-progress",
			"add VAY-43 needs-info",
			"comment VAY-43 marker=needs-info author=author-2",
		}
		if !reflect.DeepEqual(tracker.ops, wantOps) {
			t.Fatalf("reply backend ops mismatch:\n got: %#v\nwant: %#v", tracker.ops, wantOps)
		}
		if len(tracker.comments) != 1 {
			t.Fatalf("reply comments = %d, want 1", len(tracker.comments))
		}
		reply := tracker.comments[0].Body
		if !strings.Contains(reply, "<!-- yolo-runner:needs-info -->") || !strings.Contains(reply, "Simple answer: the package owner is adapta/messenger.") {
			t.Fatalf("unexpected reply comment:\n%s", reply)
		}
		for _, unwanted := range []string{"Questions:", "Which package owns this behavior?"} {
			if strings.Contains(reply, unwanted) {
				t.Fatalf("reply comment should not contain %q:\n%s", unwanted, reply)
			}
		}

		record, ok, err := state.GetPreflightWriteback(ctx, item.IdempotencyKey)
		if err != nil {
			t.Fatalf("GetPreflightWriteback() error = %v", err)
		}
		if !ok {
			t.Fatalf("expected preflight writeback state for %q", item.IdempotencyKey)
		}
		if record.IdempotencyKey != item.IdempotencyKey || record.IssueID != "VAY-43" || record.Verdict != workitem.PreflightVerdictReply || record.CommentID != "comment-1" {
			t.Fatalf("unexpected preflight writeback record: %#v", record)
		}

		if _, err := src.HandleResult(ctx, item, result); err != nil {
			t.Fatalf("HandleResult(reply duplicate) error = %v", err)
		}
		if !reflect.DeepEqual(tracker.ops, wantOps) {
			t.Fatalf("duplicate reply result should not call backend again:\n got: %#v\nwant: %#v", tracker.ops, wantOps)
		}
	})
}

func preflightWritebackItem(t *testing.T, id string, sourceRef string, idempotencyKey string, task contracts.Task) workitem.Item {
	t.Helper()
	payload, err := json.Marshal(workitem.PreflightPayload{
		Task: workitem.TaskPayloadFromTask(task),
	})
	if err != nil {
		t.Fatalf("marshal preflight payload: %v", err)
	}
	return workitem.Item{
		ID:             id,
		Kind:           workitem.KindPreflight,
		Source:         "startrek",
		SourceRef:      sourceRef,
		IdempotencyKey: idempotencyKey,
		Payload:        payload,
	}
}

func preflightWritebackResult(t *testing.T, itemID string, result workitem.PreflightResult) workqueue.Result {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal preflight result: %v", err)
	}
	return workqueue.Result{
		ItemID:  itemID,
		Status:  workqueue.ResultStatusCompleted,
		Payload: payload,
	}
}

type fakePreflightWritebackTracker struct {
	ops      []string
	comments []trackerstartrek.IssueComment
}

func (f *fakePreflightWritebackTracker) GetIssueComments(_ context.Context, issueID string) ([]trackerstartrek.IssueComment, error) {
	f.ops = append(f.ops, "comments "+issueID)
	return append([]trackerstartrek.IssueComment(nil), f.comments...), nil
}

func (f *fakePreflightWritebackTracker) RemoveLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "remove "+issueID+" "+label)
	return nil
}

func (f *fakePreflightWritebackTracker) AddLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "add "+issueID+" "+label)
	return nil
}

func (f *fakePreflightWritebackTracker) CreateIssueComment(_ context.Context, issueID string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error) {
	f.ops = append(f.ops, "comment "+issueID+" marker="+opts.Marker+" author="+opts.AuthorID)
	body := strings.TrimSpace(opts.Body)
	if marker := strings.TrimSpace(opts.Marker); marker != "" {
		body = "<!-- yolo-runner:" + marker + " -->\n\n" + body
	}
	comment := trackerstartrek.IssueComment{
		ID:        "comment-" + strconv.Itoa(len(f.comments)+1),
		Body:      body,
		CreatedAt: time.Date(2026, 6, 12, 12, 0, len(f.comments)+1, 0, time.UTC),
	}
	f.comments = append(f.comments, comment)
	return comment, nil
}

func (f *fakePreflightWritebackTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.ops = append(f.ops, "data "+taskID+" "+data["needs_info_marker"]+" "+data["needs_info_marker_comment_id"])
	return nil
}
