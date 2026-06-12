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

func TestSourceHandleImplementResultWritesStatusLabelsAndComments(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name                string
		queueStatus         workqueue.ResultStatus
		result              workitem.ImplementResult
		wantOps             []string
		wantCommentMarker   string
		wantCommentContains []string
	}{
		{
			name:        "completed",
			queueStatus: workqueue.ResultStatusCompleted,
			result: workitem.ImplementResult{
				Status:    string(contracts.RunnerResultCompleted),
				Branch:    "task/VAY-43",
				CommitSHA: "abc123",
				PRURL:     "https://arc.example.test/review/43",
			},
			wantOps: []string{
				"transition VAY-43 closed resolution=fixed",
				"remove VAY-43 yolo-agent-ready",
				"remove VAY-43 yolo-agent-in-progress",
				"remove VAY-43 yolo-agent-blocked",
				"remove VAY-43 yolo-agent-failed",
				"add VAY-43 yolo-agent-completed",
				"comment VAY-43 marker=implementation-completed author=",
			},
			wantCommentMarker: "implementation-completed",
			wantCommentContains: []string{
				"task/VAY-43",
				"abc123",
				"https://arc.example.test/review/43",
			},
		},
		{
			name:        "blocked",
			queueStatus: workqueue.ResultStatusBlocked,
			result: workitem.ImplementResult{
				Status: string(contracts.RunnerResultBlocked),
				Reason: "Waiting for the package owner.",
			},
			wantOps: []string{
				"transition VAY-43 need_info resolution=",
				"remove VAY-43 yolo-agent-ready",
				"remove VAY-43 yolo-agent-in-progress",
				"remove VAY-43 yolo-agent-completed",
				"remove VAY-43 yolo-agent-failed",
				"add VAY-43 yolo-agent-blocked",
				"comments VAY-43",
				"remove VAY-43 yolo-agent-in-progress",
				"add VAY-43 needs-info",
				"comment VAY-43 marker=needs-info author=",
			},
			wantCommentMarker: "needs-info",
			wantCommentContains: []string{
				"Waiting for the package owner.",
			},
		},
		{
			name:        "failed",
			queueStatus: workqueue.ResultStatusFailed,
			result: workitem.ImplementResult{
				Status: string(contracts.RunnerResultFailed),
				Reason: "Tests failed.",
			},
			wantOps: []string{
				"transition VAY-43 failed resolution=",
				"remove VAY-43 yolo-agent-ready",
				"remove VAY-43 yolo-agent-in-progress",
				"remove VAY-43 yolo-agent-completed",
				"remove VAY-43 yolo-agent-blocked",
				"add VAY-43 yolo-agent-failed",
				"comment VAY-43 marker=failure author=",
			},
			wantCommentMarker: "failure",
			wantCommentContains: []string{
				"Tests failed.",
			},
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

			tracker := newFakeImplementWritebackTracker()
			src := Source{
				SourceName:      "startrek",
				Tracker:         tracker,
				State:           state,
				ProcessingLabel: "yolo-agent-in-progress",
			}

			item := implementWritebackItem(t, "impl-"+tt.name, "VAY-43", "st/VAY-43/implement/rev7", workitem.ImplementPayload{
				TaskID: "VAY-43",
				Title:  "Implement leaf",
				PromptContext: workitem.ImplementPromptContext{
					ParentID: "VAY-42",
				},
			})
			result := implementWritebackResult(t, item.ID, tt.queueStatus, tt.result)

			followUps, err := src.HandleResult(ctx, item, result)
			if err != nil {
				t.Fatalf("HandleResult(%s) error = %v", tt.name, err)
			}
			if len(followUps) != 0 {
				t.Fatalf("HandleResult(%s) follow-ups = %#v, want none", tt.name, followUps)
			}
			if !reflect.DeepEqual(tracker.ops, tt.wantOps) {
				t.Fatalf("%s backend ops mismatch:\n got: %#v\nwant: %#v", tt.name, tracker.ops, tt.wantOps)
			}

			comments := tracker.commentsByIssue["VAY-43"]
			if len(comments) != 1 {
				t.Fatalf("%s comments = %d, want 1", tt.name, len(comments))
			}
			if marker := commentMarker(comments[0].Body); marker != tt.wantCommentMarker {
				t.Fatalf("%s comment marker = %q, want %q\n%s", tt.name, marker, tt.wantCommentMarker, comments[0].Body)
			}
			for _, want := range tt.wantCommentContains {
				if !strings.Contains(comments[0].Body, want) {
					t.Fatalf("%s comment missing %q:\n%s", tt.name, want, comments[0].Body)
				}
			}

			if _, err := src.HandleResult(ctx, item, result); err != nil {
				t.Fatalf("HandleResult(%s duplicate) error = %v", tt.name, err)
			}
			if !reflect.DeepEqual(tracker.ops, tt.wantOps) {
				t.Fatalf("duplicate %s result should not call backend again:\n got: %#v\nwant: %#v", tt.name, tracker.ops, tt.wantOps)
			}
		})
	}
}

func TestSourceHandleImplementResultSubmitsFinalizeOnceAndFinalizeResultComments(t *testing.T) {
	ctx := context.Background()
	state, err := OpenState(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	first := implementWritebackItem(t, "impl-1", "VAY-43", "st/VAY-43/implement/rev7", workitem.ImplementPayload{
		TaskID: "VAY-43",
		Title:  "First split child",
		PromptContext: workitem.ImplementPromptContext{
			ParentID: "VAY-42",
		},
	})
	second := implementWritebackItem(t, "impl-2", "VAY-44", "st/VAY-44/implement/rev7", workitem.ImplementPayload{
		TaskID: "VAY-44",
		Title:  "Second split child",
		PromptContext: workitem.ImplementPromptContext{
			ParentID: "VAY-42",
		},
	})
	second.Preset = "adapta"
	second.Priority = 9
	second.MaxAttempts = 5

	for _, record := range []SplitSubtaskItemRecord{
		{
			ParentIssueID:           "VAY-42",
			SplitTaskID:             "T20",
			SubtaskIssueID:          "VAY-43",
			ImplementItemID:         first.ID,
			ImplementIdempotencyKey: first.IdempotencyKey,
			SplitItemID:             "split-item",
		},
		{
			ParentIssueID:           "VAY-42",
			SplitTaskID:             "T21",
			SubtaskIssueID:          "VAY-44",
			ImplementItemID:         second.ID,
			ImplementIdempotencyKey: second.IdempotencyKey,
			SplitItemID:             "split-item",
		},
	} {
		if err := state.RecordSplitSubtaskItem(ctx, record); err != nil {
			t.Fatalf("RecordSplitSubtaskItem(%s) error = %v", record.SubtaskIssueID, err)
		}
	}

	tracker := newFakeImplementWritebackTracker()
	tracker.tasks["VAY-42"] = contracts.Task{
		ID:          "VAY-42",
		Title:       "Parent split task",
		Description: "Implement both split children.",
	}
	src := Source{
		SourceName:      "startrek",
		Tracker:         tracker,
		State:           state,
		ProcessingLabel: "yolo-agent-in-progress",
	}

	firstResult := implementWritebackResult(t, first.ID, workqueue.ResultStatusCompleted, workitem.ImplementResult{
		Status:    string(contracts.RunnerResultCompleted),
		Branch:    "task/VAY-43",
		CommitSHA: "abc123",
		PRURL:     "https://arc.example.test/review/43",
	})
	followUps, err := src.HandleResult(ctx, first, firstResult)
	if err != nil {
		t.Fatalf("HandleResult(first completed) error = %v", err)
	}
	if len(followUps) != 0 {
		t.Fatalf("first completed follow-ups = %#v, want none until all split children close", followUps)
	}

	secondResult := implementWritebackResult(t, second.ID, workqueue.ResultStatusCompleted, workitem.ImplementResult{
		Status:    string(contracts.RunnerResultCompleted),
		Branch:    "task/VAY-44",
		CommitSHA: "def456",
		PRURL:     "https://arc.example.test/review/44",
	})
	followUps, err = src.HandleResult(ctx, second, secondResult)
	if err != nil {
		t.Fatalf("HandleResult(second completed) error = %v", err)
	}
	if len(followUps) != 1 {
		t.Fatalf("second completed follow-ups = %#v, want one finalize submission", followUps)
	}
	finalize := followUps[0]
	if finalize.Kind != workitem.KindFinalize || finalize.Source != "startrek" || finalize.SourceRef != "VAY-42" {
		t.Fatalf("finalize submission identity = %#v, want startrek finalize for VAY-42", finalize)
	}
	if finalize.IdempotencyKey != "st/VAY-42/finalize/rev7" {
		t.Fatalf("finalize idempotency key = %q, want st/VAY-42/finalize/rev7", finalize.IdempotencyKey)
	}
	if finalize.Preset != second.Preset || finalize.Priority != second.Priority || finalize.MaxAttempts != second.MaxAttempts {
		t.Fatalf("finalize queue fields = preset %q priority %d max attempts %d, want preset %q priority %d max attempts %d", finalize.Preset, finalize.Priority, finalize.MaxAttempts, second.Preset, second.Priority, second.MaxAttempts)
	}

	finalizePayload, err := workitem.DecodeFinalizePayload(finalize.Payload)
	if err != nil {
		t.Fatalf("DecodeFinalizePayload() error = %v", err)
	}
	if finalizePayload.ParentRef != "VAY-42" || finalizePayload.Title != "Parent split task" {
		t.Fatalf("finalize payload parent/title = %#v, want VAY-42 Parent split task", finalizePayload)
	}
	if !reflect.DeepEqual(finalizePayload.ChildBranches, []string{"task/VAY-43", "task/VAY-44"}) {
		t.Fatalf("finalize child branches = %#v, want both child branches in split order", finalizePayload.ChildBranches)
	}

	duplicate, err := src.HandleResult(ctx, second, secondResult)
	if err != nil {
		t.Fatalf("HandleResult(second duplicate) error = %v", err)
	}
	if len(duplicate) != 0 {
		t.Fatalf("duplicate second completed follow-ups = %#v, want none", duplicate)
	}

	finalizeItem := workitem.Item{
		ID:             "finalize-item",
		Kind:           workitem.KindFinalize,
		Source:         "startrek",
		SourceRef:      "VAY-42",
		IdempotencyKey: finalize.IdempotencyKey,
		Preset:         finalize.Preset,
		Priority:       finalize.Priority,
		Payload:        finalize.Payload,
		MaxAttempts:    finalize.MaxAttempts,
	}
	finalizeResult := finalizeWritebackResult(t, finalizeItem.ID, workqueue.ResultStatusCompleted, workitem.FinalizeResult{
		PRURL: "https://arc.example.test/review/123",
	})
	if followUps, err = src.HandleResult(ctx, finalizeItem, finalizeResult); err != nil {
		t.Fatalf("HandleResult(finalize) error = %v", err)
	}
	if len(followUps) != 0 {
		t.Fatalf("finalize result follow-ups = %#v, want none", followUps)
	}
	parentComments := tracker.commentsByIssue["VAY-42"]
	if len(parentComments) != 1 {
		t.Fatalf("parent comments = %d, want one parent PR-created comment", len(parentComments))
	}
	for _, want := range []string{
		"<!-- yolo-runner:parent-pr-created -->",
		"https://arc.example.test/review/123",
		"VAY-43",
		"VAY-44",
	} {
		if !strings.Contains(parentComments[0].Body, want) {
			t.Fatalf("parent PR comment missing %q:\n%s", want, parentComments[0].Body)
		}
	}

	if _, err = src.HandleResult(ctx, finalizeItem, finalizeResult); err != nil {
		t.Fatalf("HandleResult(finalize duplicate) error = %v", err)
	}
	if len(tracker.commentsByIssue["VAY-42"]) != 1 {
		t.Fatalf("duplicate finalize result posted another parent comment: %#v", tracker.commentsByIssue["VAY-42"])
	}
}

func implementWritebackItem(t *testing.T, id string, sourceRef string, idempotencyKey string, payload workitem.ImplementPayload) workitem.Item {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal implement payload: %v", err)
	}
	return workitem.Item{
		ID:             id,
		Kind:           workitem.KindImplement,
		Source:         "startrek",
		SourceRef:      sourceRef,
		IdempotencyKey: idempotencyKey,
		Preset:         "adapta",
		Priority:       3,
		Payload:        raw,
		MaxAttempts:    4,
	}
}

func implementWritebackResult(t *testing.T, itemID string, status workqueue.ResultStatus, result workitem.ImplementResult) workqueue.Result {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal implement result: %v", err)
	}
	return workqueue.Result{
		ItemID:  itemID,
		Status:  status,
		Payload: raw,
	}
}

func finalizeWritebackResult(t *testing.T, itemID string, status workqueue.ResultStatus, result workitem.FinalizeResult) workqueue.Result {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal finalize result: %v", err)
	}
	return workqueue.Result{
		ItemID:  itemID,
		Status:  status,
		Payload: raw,
	}
}

type fakeImplementWritebackTracker struct {
	ops             []string
	commentsByIssue map[string][]trackerstartrek.IssueComment
	tasks           map[string]contracts.Task
}

func newFakeImplementWritebackTracker() *fakeImplementWritebackTracker {
	return &fakeImplementWritebackTracker{
		commentsByIssue: map[string][]trackerstartrek.IssueComment{},
		tasks:           map[string]contracts.Task{},
	}
}

func (f *fakeImplementWritebackTracker) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	task, ok := f.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return &contracts.Task{ID: strings.TrimSpace(taskID)}, nil
	}
	task.Metadata = cloneStartrekStringMap(task.Metadata)
	return &task, nil
}

func (f *fakeImplementWritebackTracker) SetTaskStatus(_ context.Context, issueID string, status contracts.TaskStatus) error {
	issueID = strings.TrimSpace(issueID)
	var addLabel string
	var transition string
	var resolution string
	switch status {
	case contracts.TaskStatusClosed:
		addLabel = "yolo-agent-completed"
		transition = "closed"
		resolution = "fixed"
	case contracts.TaskStatusBlocked:
		addLabel = "yolo-agent-blocked"
		transition = "need_info"
	case contracts.TaskStatusFailed:
		addLabel = "yolo-agent-failed"
		transition = "failed"
	default:
		addLabel = "yolo-agent-ready"
	}
	if transition != "" {
		f.ops = append(f.ops, "transition "+issueID+" "+transition+" resolution="+resolution)
	}
	for _, label := range []string{"yolo-agent-ready", "yolo-agent-in-progress", "yolo-agent-completed", "yolo-agent-blocked", "yolo-agent-failed"} {
		if label == addLabel {
			continue
		}
		f.ops = append(f.ops, "remove "+issueID+" "+label)
	}
	if addLabel != "" {
		f.ops = append(f.ops, "add "+issueID+" "+addLabel)
	}
	return nil
}

func (f *fakeImplementWritebackTracker) GetIssueComments(_ context.Context, issueID string) ([]trackerstartrek.IssueComment, error) {
	issueID = strings.TrimSpace(issueID)
	f.ops = append(f.ops, "comments "+issueID)
	return append([]trackerstartrek.IssueComment(nil), f.commentsByIssue[issueID]...), nil
}

func (f *fakeImplementWritebackTracker) RemoveLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "remove "+strings.TrimSpace(issueID)+" "+strings.TrimSpace(label))
	return nil
}

func (f *fakeImplementWritebackTracker) AddLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "add "+strings.TrimSpace(issueID)+" "+strings.TrimSpace(label))
	return nil
}

func (f *fakeImplementWritebackTracker) CreateIssueComment(_ context.Context, issueID string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error) {
	issueID = strings.TrimSpace(issueID)
	f.ops = append(f.ops, "comment "+issueID+" marker="+strings.TrimSpace(opts.Marker)+" author="+strings.TrimSpace(opts.AuthorID))
	body := strings.TrimSpace(opts.Body)
	if marker := strings.TrimSpace(opts.Marker); marker != "" {
		body = "<!-- yolo-runner:" + marker + " -->\n\n" + body
	}
	comment := trackerstartrek.IssueComment{
		ID:        "comment-" + strconv.Itoa(len(f.commentsByIssue[issueID])+1),
		Body:      body,
		CreatedAt: time.Date(2026, 6, 12, 12, 0, len(f.commentsByIssue[issueID])+1, 0, time.UTC),
	}
	f.commentsByIssue[issueID] = append(f.commentsByIssue[issueID], comment)
	return comment, nil
}

func (f *fakeImplementWritebackTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	taskID = strings.TrimSpace(taskID)
	task := f.tasks[taskID]
	task.ID = taskID
	if task.Metadata == nil {
		task.Metadata = map[string]string{}
	}
	for key, value := range data {
		task.Metadata[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	f.tasks[taskID] = task
	return nil
}

func commentMarker(body string) string {
	body = strings.TrimSpace(body)
	prefix := "<!-- yolo-runner:"
	if !strings.HasPrefix(body, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(body, prefix)
	end := strings.Index(rest, " -->")
	if end < 0 {
		return ""
	}
	return rest[:end]
}
