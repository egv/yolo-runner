package startrek

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceHandleImplementResultWritesStatusCommentsAndFinalizeOnce(t *testing.T) {
	ctx := context.Background()

	t.Run("terminal writeback table", func(t *testing.T) {
		tests := []struct {
			name              string
			taskID            string
			queueStatus       workqueue.ResultStatus
			result            workitem.ImplementResult
			wantStatus        contracts.TaskStatus
			wantData          map[string]string
			wantCommentMarker string
			wantCommentText   []string
			wantOps           []string
		}{
			{
				name:        "completed",
				taskID:      "VAY-43",
				queueStatus: workqueue.ResultStatusCompleted,
				result: workitem.ImplementResult{
					Status:        string(contracts.RunnerResultCompleted),
					Branch:        "task/VAY-43",
					CommitSHA:     "abc123",
					PRURL:         "https://arc.example.test/review/43",
					ReviewVerdict: "pass",
				},
				wantStatus:        contracts.TaskStatusClosed,
				wantCommentMarker: "implementation-completed",
				wantCommentText: []string{
					"task/VAY-43",
					"abc123",
					"https://arc.example.test/review/43",
					"pass",
				},
				wantOps: []string{
					"status VAY-43 closed",
					"comment VAY-43 marker=implementation-completed author=",
				},
			},
			{
				name:        "blocked",
				taskID:      "VAY-44",
				queueStatus: workqueue.ResultStatusBlocked,
				result: workitem.ImplementResult{
					Status: string(contracts.RunnerResultBlocked),
					Reason: "needs a product decision",
					Artifacts: map[string]string{
						"completion_retry_count": "2",
						"landing_status":         "blocked",
					},
				},
				wantStatus: contracts.TaskStatusBlocked,
				wantData: map[string]string{
					"triage_status":                "blocked",
					"triage_reason":                "needs a product decision",
					"decision":                     "blocked",
					"reason":                       "needs a product decision",
					"completion_retry_count":       "2",
					"landing_status":               "blocked",
					"needs_info_marker":            "needs-info",
					"needs_info_marker_comment_id": "comment-1",
					"needs_info_marker_created_at": "2026-06-12T12:00:01Z",
				},
				wantCommentMarker: "needs-info",
				wantCommentText: []string{
					"needs a product decision",
					"Questions:",
				},
				wantOps: []string{
					"data VAY-44 completion_retry_count=2 decision=blocked landing_status=blocked reason=needs a product decision triage_reason=needs a product decision triage_status=blocked",
					"status VAY-44 blocked",
					"remove VAY-44 yolo-agent-in-progress",
					"add VAY-44 needs-info",
					"comment VAY-44 marker=needs-info author=author-44",
					"data VAY-44 needs_info_marker=needs-info needs_info_marker_comment_id=comment-1 needs_info_marker_created_at=2026-06-12T12:00:01Z",
				},
			},
			{
				name:        "failed",
				taskID:      "VAY-45",
				queueStatus: workqueue.ResultStatusFailed,
				result: workitem.ImplementResult{
					Status:        string(contracts.RunnerResultFailed),
					Reason:        "review rejected: missing regression test",
					ReviewVerdict: "fail",
					Artifacts: map[string]string{
						"review_retry_count":   "1",
						"review_fail_feedback": "missing regression test",
					},
				},
				wantStatus: contracts.TaskStatusFailed,
				wantData: map[string]string{
					"triage_status":        "failed",
					"triage_reason":        "review rejected: missing regression test",
					"decision":             "failed",
					"reason":               "review rejected: missing regression test",
					"review_retry_count":   "1",
					"review_verdict":       "fail",
					"review_fail_feedback": "missing regression test",
				},
				wantCommentMarker: "failure",
				wantCommentText: []string{
					"review rejected: missing regression test",
				},
				wantOps: []string{
					"data VAY-45 decision=failed reason=review rejected: missing regression test review_fail_feedback=missing regression test review_retry_count=1 review_verdict=fail triage_reason=review rejected: missing regression test triage_status=failed",
					"status VAY-45 failed",
					"comment VAY-45 marker=failure author=",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				state := openStartrekSourceState(t)
				tracker := &fakeImplementWritebackTracker{
					tasks: map[string]contracts.Task{
						tt.taskID: {
							ID:          tt.taskID,
							Title:       tt.name + " task",
							Description: "Author: Example (" + strings.ToLower(strings.ReplaceAll(tt.taskID, "VAY-", "author-")) + ")",
							Status:      contracts.TaskStatusInProgress,
							Metadata:    map[string]string{},
						},
					},
				}
				src := Source{
					SourceName:      "startrek",
					Tracker:         tracker,
					State:           state,
					ProcessingLabel: "yolo-agent-in-progress",
					NeedsInfoLabel:  "needs-info",
				}

				item := implementWritebackItem(t, "item-"+tt.taskID, tt.taskID, "st/"+tt.taskID+"/implement/rev7", contracts.Task{
					ID:          tt.taskID,
					Title:       tt.name + " task",
					Description: "Author: Example (" + strings.ToLower(strings.ReplaceAll(tt.taskID, "VAY-", "author-")) + ")",
				})
				followUps, err := src.HandleResult(ctx, item, implementWritebackResult(t, item.ID, tt.queueStatus, tt.result))
				if err != nil {
					t.Fatalf("HandleResult(%s) error = %v", tt.name, err)
				}
				if len(followUps) != 0 {
					t.Fatalf("HandleResult(%s) follow-ups = %#v, want none", tt.name, followUps)
				}
				if got := tracker.tasks[tt.taskID].Status; got != tt.wantStatus {
					t.Fatalf("status = %q, want %q", got, tt.wantStatus)
				}
				if !reflect.DeepEqual(tracker.data[tt.taskID], tt.wantData) {
					t.Fatalf("task data mismatch:\n got: %#v\nwant: %#v", tracker.data[tt.taskID], tt.wantData)
				}
				if !reflect.DeepEqual(tracker.ops, tt.wantOps) {
					t.Fatalf("ops mismatch:\n got: %#v\nwant: %#v", tracker.ops, tt.wantOps)
				}
				if len(tracker.comments) != 1 {
					t.Fatalf("comments = %d, want 1", len(tracker.comments))
				}
				comment := tracker.comments[0]
				if comment.issueID != tt.taskID || comment.opts.Marker != tt.wantCommentMarker {
					t.Fatalf("comment = issue %q marker %q, want issue %q marker %q", comment.issueID, comment.opts.Marker, tt.taskID, tt.wantCommentMarker)
				}
				for _, want := range tt.wantCommentText {
					if !strings.Contains(comment.opts.Body, want) {
						t.Fatalf("comment body missing %q:\n%s", want, comment.opts.Body)
					}
				}

				duplicate, err := src.HandleResult(ctx, item, implementWritebackResult(t, item.ID, tt.queueStatus, tt.result))
				if err != nil {
					t.Fatalf("HandleResult(%s duplicate) error = %v", tt.name, err)
				}
				if len(duplicate) != 0 {
					t.Fatalf("duplicate follow-ups = %#v, want none", duplicate)
				}
				if len(tracker.comments) != 1 {
					t.Fatalf("duplicate writeback posted another comment; comments = %d", len(tracker.comments))
				}
				if !reflect.DeepEqual(tracker.ops, tt.wantOps) {
					t.Fatalf("duplicate writeback changed ops:\n got: %#v\nwant: %#v", tracker.ops, tt.wantOps)
				}
			})
		}
	})

	t.Run("finalize chain fires once and posts parent PR comment", func(t *testing.T) {
		state := openStartrekSourceState(t)
		tracker := &fakeImplementWritebackTracker{
			tasks: map[string]contracts.Task{
				"VAY-42": {
					ID:          "VAY-42",
					Title:       "Parent split task",
					Description: "Ship all split children.",
					Status:      contracts.TaskStatusInProgress,
					Metadata:    map[string]string{},
				},
				"VAY-43": {
					ID:       "VAY-43",
					Title:    "First child",
					Status:   contracts.TaskStatusClosed,
					ParentID: "VAY-42",
					Metadata: map[string]string{"branch": "task/VAY-43"},
				},
				"VAY-44": {
					ID:       "VAY-44",
					Title:    "Second child",
					Status:   contracts.TaskStatusInProgress,
					ParentID: "VAY-42",
					Metadata: map[string]string{},
				},
			},
		}
		if err := state.RecordSplitSubtaskItem(ctx, SplitSubtaskItemRecord{
			ParentIssueID:           "VAY-42",
			SplitTaskID:             "T20",
			SubtaskIssueID:          "VAY-43",
			ImplementItemID:         "implement-43",
			ImplementIdempotencyKey: "st/VAY-43/implement/rev7",
			SplitItemID:             "split-item",
		}); err != nil {
			t.Fatalf("record first split subtask item: %v", err)
		}
		if err := state.RecordSplitSubtaskItem(ctx, SplitSubtaskItemRecord{
			ParentIssueID:           "VAY-42",
			SplitTaskID:             "T21",
			SubtaskIssueID:          "VAY-44",
			ImplementItemID:         "implement-44",
			ImplementIdempotencyKey: "st/VAY-44/implement/rev7",
			SplitItemID:             "split-item",
		}); err != nil {
			t.Fatalf("record second split subtask item: %v", err)
		}

		src := Source{
			SourceName: "startrek",
			Tracker:    tracker,
			State:      state,
		}
		item := implementWritebackItem(t, "implement-44", "VAY-44", "st/VAY-44/implement/rev7", contracts.Task{
			ID:       "VAY-44",
			Title:    "Second child",
			ParentID: "VAY-42",
		})
		item.Preset = "adapta"
		item.Priority = 5
		item.MaxAttempts = 4
		implementResult := workitem.ImplementResult{
			Status:    string(contracts.RunnerResultCompleted),
			Branch:    "task/VAY-44",
			CommitSHA: "def456",
			PRURL:     "https://arc.example.test/review/44",
		}

		followUps, err := src.HandleResult(ctx, item, implementWritebackResult(t, item.ID, workqueue.ResultStatusCompleted, implementResult))
		if err != nil {
			t.Fatalf("HandleResult(implement completed) error = %v", err)
		}
		if len(followUps) != 1 {
			t.Fatalf("finalize follow-ups = %#v, want one", followUps)
		}
		finalize := followUps[0]
		if finalize.Kind != workitem.KindFinalize || finalize.SourceRef != "VAY-42" || finalize.IdempotencyKey != "st/VAY-42/finalize/rev7" {
			t.Fatalf("finalize follow-up = kind %q ref %q key %q, want finalize VAY-42 rev7", finalize.Kind, finalize.SourceRef, finalize.IdempotencyKey)
		}
		if finalize.Preset != "adapta" || finalize.Priority != 5 || finalize.MaxAttempts != 4 {
			t.Fatalf("finalize queue fields = preset %q priority %d attempts %d", finalize.Preset, finalize.Priority, finalize.MaxAttempts)
		}
		finalizePayload, err := workitem.DecodeFinalizePayload(finalize.Payload)
		if err != nil {
			t.Fatalf("decode finalize payload: %v", err)
		}
		if finalizePayload.ParentRef != "VAY-42" || finalizePayload.Title != "Parent split task" {
			t.Fatalf("finalize payload parent/title = %#v", finalizePayload)
		}
		if !reflect.DeepEqual(finalizePayload.ChildBranches, []string{"task/VAY-43", "task/VAY-44"}) {
			t.Fatalf("finalize child branches = %#v, want both child branches", finalizePayload.ChildBranches)
		}

		duplicate, err := src.HandleResult(ctx, item, implementWritebackResult(t, item.ID, workqueue.ResultStatusCompleted, implementResult))
		if err != nil {
			t.Fatalf("HandleResult(implement duplicate) error = %v", err)
		}
		if len(duplicate) != 0 {
			t.Fatalf("duplicate implement follow-ups = %#v, want none", duplicate)
		}
		if got := countCommentsWithMarker(tracker.comments, "implementation-completed"); got != 1 {
			t.Fatalf("implementation completion comments = %d, want 1", got)
		}

		finalizeItem := workitem.Item{
			ID:             "finalize-item",
			Kind:           workitem.KindFinalize,
			Source:         "startrek",
			SourceRef:      "VAY-42",
			IdempotencyKey: finalize.IdempotencyKey,
			Payload:        finalize.Payload,
		}
		finalizeResult := finalizeWritebackResult(t, finalizeItem.ID, workitem.FinalizeResult{PRURL: "https://arc.example.test/review/parent"})
		finalizeFollowUps, err := src.HandleResult(ctx, finalizeItem, finalizeResult)
		if err != nil {
			t.Fatalf("HandleResult(finalize) error = %v", err)
		}
		if len(finalizeFollowUps) != 0 {
			t.Fatalf("finalize result follow-ups = %#v, want none", finalizeFollowUps)
		}
		parentData := tracker.data["VAY-42"]
		if parentData["parent_pr_created"] != "true" || parentData["parent_pr_url"] != "https://arc.example.test/review/parent" || parentData["pr_url"] != "https://arc.example.test/review/parent" {
			t.Fatalf("parent PR data = %#v", parentData)
		}
		if got := countCommentsWithMarker(tracker.comments, "parent-pr-created"); got != 1 {
			t.Fatalf("parent PR comments = %d, want 1", got)
		}
		parentComment := latestCommentWithMarker(tracker.comments, "parent-pr-created")
		for _, want := range []string{"https://arc.example.test/review/parent", "VAY-43", "VAY-44"} {
			if !strings.Contains(parentComment.opts.Body, want) {
				t.Fatalf("parent PR comment missing %q:\n%s", want, parentComment.opts.Body)
			}
		}

		if _, err := src.HandleResult(ctx, finalizeItem, finalizeResult); err != nil {
			t.Fatalf("HandleResult(finalize duplicate) error = %v", err)
		}
		if got := countCommentsWithMarker(tracker.comments, "parent-pr-created"); got != 1 {
			t.Fatalf("duplicate finalize posted parent PR comments = %d, want 1", got)
		}
	})
}

func openStartrekSourceState(t *testing.T) *StateStore {
	t.Helper()

	state, err := OpenState(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Errorf("state.Close() error = %v", err)
		}
	})
	return state
}

func implementWritebackItem(t *testing.T, id string, sourceRef string, idempotencyKey string, task contracts.Task) workitem.Item {
	t.Helper()
	payload, err := json.Marshal(workitem.ImplementPayload{
		TaskID:      task.ID,
		Title:       task.Title,
		Description: task.Description,
		PromptContext: workitem.ImplementPromptContext{
			ParentID: task.ParentID,
			Metadata: cloneStartrekStringMap(task.Metadata),
		},
	})
	if err != nil {
		t.Fatalf("marshal implement payload: %v", err)
	}
	return workitem.Item{
		ID:             id,
		Kind:           workitem.KindImplement,
		Source:         "startrek",
		SourceRef:      sourceRef,
		IdempotencyKey: idempotencyKey,
		Payload:        payload,
	}
}

func implementWritebackResult(t *testing.T, itemID string, status workqueue.ResultStatus, result workitem.ImplementResult) workqueue.Result {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal implement result: %v", err)
	}
	return workqueue.Result{
		ItemID:  itemID,
		Status:  status,
		Payload: payload,
	}
}

func finalizeWritebackResult(t *testing.T, itemID string, result workitem.FinalizeResult) workqueue.Result {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal finalize result: %v", err)
	}
	return workqueue.Result{
		ItemID:  itemID,
		Status:  workqueue.ResultStatusCompleted,
		Payload: payload,
	}
}

type fakeImplementWritebackTracker struct {
	tasks    map[string]contracts.Task
	data     map[string]map[string]string
	comments []fakeImplementComment
	ops      []string
}

type fakeImplementComment struct {
	issueID string
	opts    trackerstartrek.IssueCommentCreateOptions
}

func (f *fakeImplementWritebackTracker) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return &contracts.Task{ID: taskID, Metadata: map[string]string{}}, nil
	}
	task.Metadata = cloneStartrekStringMap(task.Metadata)
	return &task, nil
}

func (f *fakeImplementWritebackTracker) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	f.ops = append(f.ops, "status "+taskID+" "+string(status))
	if f.tasks == nil {
		f.tasks = map[string]contracts.Task{}
	}
	task := f.tasks[taskID]
	task.ID = taskID
	task.Status = status
	f.tasks[taskID] = task
	return nil
}

func (f *fakeImplementWritebackTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if f.data == nil {
		f.data = map[string]map[string]string{}
	}
	merged := cloneStartrekStringMap(f.data[taskID])
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range data {
		if strings.TrimSpace(value) != "" {
			merged[key] = strings.TrimSpace(value)
		}
	}
	f.data[taskID] = merged
	if f.tasks == nil {
		f.tasks = map[string]contracts.Task{}
	}
	task := f.tasks[taskID]
	task.ID = taskID
	task.Metadata = cloneStartrekStringMap(merged)
	f.tasks[taskID] = task
	f.ops = append(f.ops, "data "+taskID+" "+formatImplementDataForTest(data))
	return nil
}

func (f *fakeImplementWritebackTracker) GetIssueComments(_ context.Context, issueID string) ([]trackerstartrek.IssueComment, error) {
	var comments []trackerstartrek.IssueComment
	for i, call := range f.comments {
		if call.issueID != issueID {
			continue
		}
		body := call.opts.Body
		if marker := strings.TrimSpace(call.opts.Marker); marker != "" {
			body = "<!-- yolo-runner:" + marker + " -->\n\n" + body
		}
		comments = append(comments, trackerstartrek.IssueComment{
			ID:        "comment-" + strconvForTest(i+1),
			Body:      body,
			CreatedAt: time.Date(2026, 6, 12, 12, 0, i+1, 0, time.UTC),
		})
	}
	return comments, nil
}

func (f *fakeImplementWritebackTracker) RemoveLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "remove "+issueID+" "+label)
	return nil
}

func (f *fakeImplementWritebackTracker) AddLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "add "+issueID+" "+label)
	return nil
}

func (f *fakeImplementWritebackTracker) CreateIssueComment(_ context.Context, issueID string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error) {
	f.ops = append(f.ops, "comment "+issueID+" marker="+opts.Marker+" author="+opts.AuthorID)
	f.comments = append(f.comments, fakeImplementComment{
		issueID: issueID,
		opts:    opts,
	})
	body := opts.Body
	if marker := strings.TrimSpace(opts.Marker); marker != "" {
		body = "<!-- yolo-runner:" + marker + " -->\n\n" + body
	}
	return trackerstartrek.IssueComment{
		ID:        "comment-" + strconvForTest(len(f.comments)),
		Body:      body,
		CreatedAt: time.Date(2026, 6, 12, 12, 0, len(f.comments), 0, time.UTC),
	}, nil
}

func formatImplementDataForTest(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+data[key])
	}
	return strings.Join(parts, " ")
}

func countCommentsWithMarker(comments []fakeImplementComment, marker string) int {
	count := 0
	for _, comment := range comments {
		if comment.opts.Marker == marker {
			count++
		}
	}
	return count
}

func latestCommentWithMarker(comments []fakeImplementComment, marker string) fakeImplementComment {
	for i := len(comments) - 1; i >= 0; i-- {
		if comments[i].opts.Marker == marker {
			return comments[i]
		}
	}
	return fakeImplementComment{}
}

func strconvForTest(value int) string {
	return strings.TrimSpace(strconv.Itoa(value))
}
