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

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceHandleSplitResultCreatesSubtasksEnqueuesImplementDepsAndRecordsState(t *testing.T) {
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
	queue, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("workqueue.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := queue.Close(); err != nil {
			t.Errorf("queue.Close() error = %v", err)
		}
	})

	tracker := &fakeSplitWritebackTracker{
		issueIDs: []string{"VAY-43", "VAY-44"},
		tasks: map[string]contracts.Task{
			"VAY-42": {ID: "VAY-42", Metadata: map[string]string{}},
		},
	}
	src := Source{
		SourceName:   "startrek",
		Tracker:      tracker,
		State:        state,
		Queue:        queue,
		ReadyLabel:   "yolo-agent-ready",
		SubtaskLabel: "agent:subtask",
		SplitVersion: "strict-v1",
	}

	parent := contracts.Task{
		ID:          "VAY-42",
		Title:       "Split queue source work",
		Description: "Create queue-native Startrek split result writeback.",
		ParentID:    "VAY",
		Metadata:    map[string]string{"component": "sourcehost"},
	}
	queueRoot := contracts.Task{ID: "VAY", Title: "Queue root"}
	item := splitWritebackItem(t, "split-item", "VAY-42", "st/VAY-42/split/rev7", parent, queueRoot)
	item.Preset = "adapta"
	item.Priority = 7
	item.MaxAttempts = 5
	result := splitWritebackResult(t, item.ID, workitem.SplitResult{
		Tasks: []splitter.Task{
			{
				ID:            "T20",
				Title:         "Create split writeback test",
				Why:           []string{"Lock the source behavior before implementation."},
				InScope:       []string{"Exercise Startrek split result handling."},
				StrictTDD:     []string{"Add failing test first."},
				DoneWhen:      []string{"Split result produces implementation items."},
				ExpectedFiles: []string{"internal/sources/startrek/split_writeback.go"},
			},
			{
				ID:            "T21",
				Title:         "Implement split writeback",
				Why:           []string{"Sourcehost needs queue-native split follow-ups."},
				InScope:       []string{"Create implement submissions from subtasks."},
				StrictTDD:     []string{"Make the targeted test pass."},
				DoneWhen:      []string{"Queue dependencies mirror the splitter order."},
				ExpectedFiles: []string{"internal/sources/startrek/split_writeback.go"},
			},
		},
		Order: []splitter.Dependency{{From: "T20", To: "T21"}},
	})

	followUps, err := src.HandleResult(ctx, item, result)
	if err != nil {
		t.Fatalf("HandleResult(split) error = %v", err)
	}
	if !tracker.hasRemovedLabel("VAY-42", "yolo-agent-ready") {
		t.Fatalf("split writeback did not remove parent ready label; removals=%#v", tracker.removedLabels)
	}
	assertSplitImplementFollowUps(t, followUps, []string{
		"st/VAY-43/implement/rev7",
		"st/VAY-44/implement/rev7",
	})

	if got, want := len(tracker.creates), 2; got != want {
		t.Fatalf("created subtasks = %d, want %d", got, want)
	}
	if !containsString(tracker.creates[0].Labels, "yolo-agent-ready") || !containsString(tracker.creates[0].Labels, "agent:subtask") {
		t.Fatalf("first subtask labels = %#v, want ready and subtask labels", tracker.creates[0].Labels)
	}
	if !containsString(tracker.creates[1].Labels, "depends-on:VAY-43") {
		t.Fatalf("second subtask labels = %#v, want depends-on:VAY-43", tracker.creates[1].Labels)
	}

	first, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if first == nil {
		t.Fatalf("Claim(first) returned nil")
	}
	if first.SourceRef != "VAY-43" || first.IdempotencyKey != "st/VAY-43/implement/rev7" {
		t.Fatalf("first claimed item = source_ref %q key %q, want VAY-43 implement", first.SourceRef, first.IdempotencyKey)
	}
	blocked, err := queue.Claim("runner-b", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(blocked second) error = %v", err)
	}
	if blocked != nil {
		t.Fatalf("second item claimed before dependency completed: %#v", blocked)
	}

	firstRecord, ok, err := state.GetSplitSubtaskItem(ctx, "VAY-42", "VAY-43")
	if err != nil {
		t.Fatalf("GetSplitSubtaskItem(first) error = %v", err)
	}
	if !ok {
		t.Fatalf("missing split subtask item mapping for VAY-43")
	}
	if firstRecord.ImplementItemID != first.ID || firstRecord.SplitTaskID != "T20" || firstRecord.ImplementIdempotencyKey != "st/VAY-43/implement/rev7" {
		t.Fatalf("unexpected first split subtask item record: %#v, claimed item %q", firstRecord, first.ID)
	}

	if err := queue.Complete(first.ID, workqueue.Result{Payload: json.RawMessage(`{"status":"completed"}`)}); err != nil {
		t.Fatalf("Complete(first) error = %v", err)
	}
	second, err := queue.Claim("runner-c", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(second) error = %v", err)
	}
	if second == nil {
		t.Fatalf("Claim(second) returned nil after dependency completed")
	}
	if second.SourceRef != "VAY-44" || second.IdempotencyKey != "st/VAY-44/implement/rev7" {
		t.Fatalf("second claimed item = source_ref %q key %q, want VAY-44 implement", second.SourceRef, second.IdempotencyKey)
	}

	secondRecord, ok, err := state.GetSplitSubtaskItem(ctx, "VAY-42", "VAY-44")
	if err != nil {
		t.Fatalf("GetSplitSubtaskItem(second) error = %v", err)
	}
	if !ok {
		t.Fatalf("missing split subtask item mapping for VAY-44")
	}
	if secondRecord.ImplementItemID != second.ID || secondRecord.SplitTaskID != "T21" || secondRecord.ImplementIdempotencyKey != "st/VAY-44/implement/rev7" {
		t.Fatalf("unexpected second split subtask item record: %#v, claimed item %q", secondRecord, second.ID)
	}

	duplicate, err := src.HandleResult(ctx, item, result)
	if err != nil {
		t.Fatalf("HandleResult(split duplicate) error = %v", err)
	}
	if !reflect.DeepEqual(duplicate, followUps) {
		t.Fatalf("duplicate split follow-ups mismatch:\n got: %#v\nwant: %#v", duplicate, followUps)
	}
	if got, want := len(tracker.creates), 2; got != want {
		t.Fatalf("duplicate split result created more subtasks: got %d creates want %d", got, want)
	}
}

func splitWritebackItem(t *testing.T, id string, sourceRef string, idempotencyKey string, task contracts.Task, queueRoot contracts.Task) workitem.Item {
	t.Helper()
	payload, err := json.Marshal(workitem.SplitPayload{
		Task:      workitem.TaskPayloadFromTask(task),
		QueueRoot: workitem.TaskPayloadFromTask(queueRoot),
	})
	if err != nil {
		t.Fatalf("marshal split payload: %v", err)
	}
	return workitem.Item{
		ID:             id,
		Kind:           workitem.KindSplit,
		Source:         "startrek",
		SourceRef:      sourceRef,
		IdempotencyKey: idempotencyKey,
		Payload:        payload,
	}
}

func splitWritebackResult(t *testing.T, itemID string, result workitem.SplitResult) workqueue.Result {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal split result: %v", err)
	}
	return workqueue.Result{
		ItemID:  itemID,
		Status:  workqueue.ResultStatusCompleted,
		Payload: payload,
	}
}

func assertSplitImplementFollowUps(t *testing.T, followUps []workqueue.Submission, wantKeys []string) {
	t.Helper()
	if len(followUps) != len(wantKeys) {
		t.Fatalf("split follow-ups = %#v, want %d implement submissions", followUps, len(wantKeys))
	}
	for i, followUp := range followUps {
		if followUp.Kind != workitem.KindImplement {
			t.Fatalf("follow-up %d kind = %q, want implement", i, followUp.Kind)
		}
		if followUp.Source != "startrek" {
			t.Fatalf("follow-up %d source = %q, want startrek", i, followUp.Source)
		}
		if followUp.IdempotencyKey != wantKeys[i] {
			t.Fatalf("follow-up %d key = %q, want %q", i, followUp.IdempotencyKey, wantKeys[i])
		}
		payload, err := workitem.DecodeImplementPayload(followUp.Payload)
		if err != nil {
			t.Fatalf("decode follow-up %d implement payload: %v", i, err)
		}
		if payload.TaskID != followUp.SourceRef {
			t.Fatalf("follow-up %d payload task ID = %q, want source ref %q", i, payload.TaskID, followUp.SourceRef)
		}
		if payload.PromptContext.ParentID != "VAY-42" {
			t.Fatalf("follow-up %d parent ID = %q, want VAY-42", i, payload.PromptContext.ParentID)
		}
		if !strings.Contains(payload.Description, "### Task:") {
			t.Fatalf("follow-up %d description does not look like split subtask body:\n%s", i, payload.Description)
		}
	}
}

type fakeSplitWritebackTracker struct {
	issueIDs      []string
	creates       []trackerstartrek.IssueCreateOptions
	tasks         map[string]contracts.Task
	comments      []trackerstartrek.IssueComment
	removedLabels []splitWritebackLabelCall
}

func (f *fakeSplitWritebackTracker) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return &contracts.Task{ID: taskID, Metadata: map[string]string{}}, nil
	}
	task.Metadata = cloneStartrekStringMap(task.Metadata)
	return &task, nil
}

func (f *fakeSplitWritebackTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if f.tasks == nil {
		f.tasks = map[string]contracts.Task{}
	}
	task := f.tasks[taskID]
	task.ID = taskID
	task.Metadata = cloneStartrekStringMap(data)
	f.tasks[taskID] = task
	return nil
}

func (f *fakeSplitWritebackTracker) CreateIssue(_ context.Context, opts trackerstartrek.IssueCreateOptions) (trackerstartrek.Issue, error) {
	opts.Labels = append([]string(nil), opts.Labels...)
	f.creates = append(f.creates, opts)
	issueID := f.issueIDs[len(f.creates)-1]
	return trackerstartrek.Issue{
		ID:          issueID,
		Title:       opts.Title,
		Description: opts.Description,
		Labels:      append([]string(nil), opts.Labels...),
		ParentID:    opts.ParentID,
	}, nil
}

func (f *fakeSplitWritebackTracker) GetIssueComments(_ context.Context, issueID string) ([]trackerstartrek.IssueComment, error) {
	var comments []trackerstartrek.IssueComment
	for _, comment := range f.comments {
		if strings.TrimSpace(comment.ID) != "" || issueID != "" {
			comments = append(comments, comment)
		}
	}
	return comments, nil
}

func (f *fakeSplitWritebackTracker) RemoveLabel(_ context.Context, issueID string, label string) error {
	f.removedLabels = append(f.removedLabels, splitWritebackLabelCall{IssueID: issueID, Label: label})
	return nil
}

func (f *fakeSplitWritebackTracker) AddLabel(context.Context, string, string) error {
	return nil
}

func (f *fakeSplitWritebackTracker) CreateIssueComment(_ context.Context, _ string, opts trackerstartrek.IssueCommentCreateOptions) (trackerstartrek.IssueComment, error) {
	body := strings.TrimSpace(opts.Body)
	if marker := strings.TrimSpace(opts.Marker); marker != "" {
		body = "<!-- yolo-runner:" + marker + " -->\n" + body
	}
	comment := trackerstartrek.IssueComment{
		ID:        "comment-" + strconv.Itoa(len(f.comments)+1),
		Body:      body,
		CreatedAt: time.Date(2026, 6, 12, 12, 0, len(f.comments)+1, 0, time.UTC),
	}
	f.comments = append(f.comments, comment)
	return comment, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type splitWritebackLabelCall struct {
	IssueID string
	Label   string
}

func (f *fakeSplitWritebackTracker) hasRemovedLabel(issueID string, label string) bool {
	for _, call := range f.removedLabels {
		if call.IssueID == issueID && call.Label == label {
			return true
		}
	}
	return false
}
