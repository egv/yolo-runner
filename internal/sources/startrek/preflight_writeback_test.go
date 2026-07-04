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
			"transition VAY-42 blocked",
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
			"transition VAY-43 blocked",
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

func TestSourceHandlePreflightReadySubmitsFollowUpAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	queueRoot := contracts.Task{
		ID:     "VAY",
		Title:  "Queue root",
		Status: contracts.TaskStatusOpen,
	}

	tests := []struct {
		name    string
		task    contracts.Task
		want    workitem.Kind
		wantKey string
	}{
		{
			name: "decomposable parent submits split",
			task: contracts.Task{
				ID:          "VAY-42",
				Title:       "Split parent task",
				Description: "Break the parent into executable subtasks.",
				Status:      contracts.TaskStatusOpen,
				ParentID:    "VAY",
				Metadata:    map[string]string{"component": "sourcehost"},
			},
			want:    workitem.KindSplit,
			wantKey: "st/VAY-42/split/rev7",
		},
		{
			name: "leaf submits implement",
			task: contracts.Task{
				ID:          "VAY-43",
				Title:       "Implement leaf task",
				Description: "Implement the already split leaf task.",
				Status:      contracts.TaskStatusOpen,
				ParentID:    "VAY-42",
				Metadata:    map[string]string{"component": "executor"},
			},
			want:    workitem.KindImplement,
			wantKey: "st/VAY-43/implement/rev7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
				SourceName:      "startrek",
				Tracker:         tracker,
				State:           state,
				ProcessingLabel: "yolo-agent-in-progress",
			}

			item := preflightWritebackItemWithQueueRoot(t, "item-ready-"+tt.task.ID, tt.task.ID, "st/"+tt.task.ID+"/preflight/rev7", tt.task, queueRoot)
			item.Preset = "adapta"
			item.Priority = 3
			item.MaxAttempts = 4
			result := preflightWritebackResult(t, item.ID, workitem.PreflightResult{
				Verdict:    workitem.PreflightVerdictReady,
				Confidence: 0.96,
				Summary:    "Ready for the next stage.",
			})

			followUps, err := src.HandleResult(ctx, item, result)
			if err != nil {
				t.Fatalf("HandleResult(ready) error = %v", err)
			}
			assertReadyFollowUp(t, followUps, tt.want, tt.wantKey, item, tt.task, queueRoot)

			wantOps := []string{
				"remove " + tt.task.ID + " yolo-agent-in-progress",
				"add " + tt.task.ID + " yolo-agent-ready",
			}
			if !reflect.DeepEqual(tracker.ops, wantOps) {
				t.Fatalf("ready backend ops mismatch:\n got: %#v\nwant: %#v", tracker.ops, wantOps)
			}

			duplicate, err := src.HandleResult(ctx, item, result)
			if err != nil {
				t.Fatalf("HandleResult(ready duplicate) error = %v", err)
			}
			assertReadyFollowUp(t, duplicate, tt.want, tt.wantKey, item, tt.task, queueRoot)
			if !reflect.DeepEqual(duplicate, followUps) {
				t.Fatalf("duplicate ready follow-up mismatch:\n got: %#v\nwant: %#v", duplicate, followUps)
			}
		})
	}
}

func preflightWritebackItem(t *testing.T, id string, sourceRef string, idempotencyKey string, task contracts.Task) workitem.Item {
	t.Helper()
	return preflightWritebackItemWithQueueRoot(t, id, sourceRef, idempotencyKey, task, contracts.Task{})
}

func preflightWritebackItemWithQueueRoot(t *testing.T, id string, sourceRef string, idempotencyKey string, task contracts.Task, queueRoot contracts.Task) workitem.Item {
	t.Helper()
	payload, err := json.Marshal(workitem.PreflightPayload{
		Task:      workitem.TaskPayloadFromTask(task),
		QueueRoot: workitem.TaskPayloadFromTask(queueRoot),
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

func assertReadyFollowUp(t *testing.T, followUps []workqueue.Submission, wantKind workitem.Kind, wantKey string, sourceItem workitem.Item, task contracts.Task, queueRoot contracts.Task) {
	t.Helper()
	if len(followUps) != 1 {
		t.Fatalf("ready follow-ups = %#v, want exactly one", followUps)
	}
	followUp := followUps[0]
	if followUp.Kind != wantKind {
		t.Fatalf("ready follow-up kind = %q, want %q", followUp.Kind, wantKind)
	}
	if followUp.Source != "startrek" {
		t.Fatalf("ready follow-up source = %q, want startrek", followUp.Source)
	}
	if followUp.SourceRef != task.ID {
		t.Fatalf("ready follow-up source ref = %q, want %q", followUp.SourceRef, task.ID)
	}
	if followUp.IdempotencyKey != wantKey {
		t.Fatalf("ready follow-up idempotency key = %q, want %q", followUp.IdempotencyKey, wantKey)
	}
	if followUp.Preset != sourceItem.Preset || followUp.Priority != sourceItem.Priority || followUp.MaxAttempts != sourceItem.MaxAttempts {
		t.Fatalf("ready follow-up queue fields = preset %q priority %d max attempts %d, want preset %q priority %d max attempts %d", followUp.Preset, followUp.Priority, followUp.MaxAttempts, sourceItem.Preset, sourceItem.Priority, sourceItem.MaxAttempts)
	}

	switch wantKind {
	case workitem.KindSplit:
		var payload workitem.SplitPayload
		if err := json.Unmarshal(followUp.Payload, &payload); err != nil {
			t.Fatalf("decode split follow-up payload: %v", err)
		}
		if !reflect.DeepEqual(payload.Task, workitem.TaskPayloadFromTask(task)) {
			t.Fatalf("split follow-up task payload = %#v, want %#v", payload.Task, workitem.TaskPayloadFromTask(task))
		}
		if !reflect.DeepEqual(payload.QueueRoot, workitem.TaskPayloadFromTask(queueRoot)) {
			t.Fatalf("split follow-up queue root payload = %#v, want %#v", payload.QueueRoot, workitem.TaskPayloadFromTask(queueRoot))
		}
	case workitem.KindImplement:
		payload, err := workitem.DecodeImplementPayload(followUp.Payload)
		if err != nil {
			t.Fatalf("decode implement follow-up payload: %v", err)
		}
		if payload.TaskID != task.ID || payload.Title != task.Title || payload.Description != task.Description {
			t.Fatalf("implement follow-up task payload = %#v, want task %#v", payload, task)
		}
		if payload.PromptContext.ParentID != task.ParentID {
			t.Fatalf("implement follow-up parent ID = %q, want %q", payload.PromptContext.ParentID, task.ParentID)
		}
		if !reflect.DeepEqual(payload.PromptContext.Metadata, task.Metadata) {
			t.Fatalf("implement follow-up metadata = %#v, want %#v", payload.PromptContext.Metadata, task.Metadata)
		}
	default:
		t.Fatalf("unsupported expected follow-up kind %q", wantKind)
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

func (f *fakePreflightWritebackTracker) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	f.ops = append(f.ops, "transition "+taskID+" "+string(status))
	return nil
}
