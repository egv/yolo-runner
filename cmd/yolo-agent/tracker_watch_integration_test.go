package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	enginepkg "github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/startrek"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
)

func TestTrackerWatchSplitToPRIntegrationCreatesOneParentPRComment(t *testing.T) {
	ctx := context.Background()
	repoRoot := t.TempDir()
	runner := &trackerWatchSplitPRRunner{}
	storage := newTrackerWatchSplitPRStorage()

	parent, err := storage.GetTask(ctx, "VAY-42")
	if err != nil {
		t.Fatalf("get parent task: %v", err)
	}
	preflightResult, err := preflight.NewRunner(runner).Run(ctx, preflight.RunInput{
		Task:      *parent,
		QueueRoot: contracts.Task{ID: "VAY", Title: "VAY", Status: contracts.TaskStatusOpen},
		Model:     "fake-codex",
		RepoRoot:  repoRoot,
		Metadata: map[string]string{
			"phase":   "preflight",
			"tracker": trackerTypeStartrek,
		},
	})
	if err != nil {
		t.Fatalf("run ready preflight: %v", err)
	}
	if preflightResult.Decision != preflight.DecisionReady {
		t.Fatalf("expected ready preflight decision, got %#v", preflightResult)
	}

	splitOutput, err := splitter.NewRunner(runner).Run(ctx, splitter.RunInput{
		Task:      *parent,
		QueueRoot: contracts.Task{ID: "VAY", Title: "VAY", Status: contracts.TaskStatusOpen},
		Model:     "fake-codex",
		RepoRoot:  repoRoot,
		Metadata: map[string]string{
			"phase":   "split",
			"tracker": trackerTypeStartrek,
		},
	})
	if err != nil {
		t.Fatalf("run strict splitter: %v", err)
	}
	if got, want := splitTaskIDs(splitOutput.Tasks), []string{"T36.1", "T36.2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected split task IDs: got %v want %v", got, want)
	}

	splitResult, err := (startrek.IdempotentSplitSubtaskCreationService{
		Tracker:      storage,
		ReadyLabel:   "yolo-agent-ready",
		SplitVersion: "strict-v1",
	}).Create(ctx, startrek.SplitSubtasksInput{
		QueueKey: "VAY",
		ParentID: "VAY-42",
		Output:   splitOutput,
	})
	if err != nil {
		t.Fatalf("create split subtasks: %v", err)
	}
	if got, want := splitIssueIDs(splitResult.Issues), []string{"VAY-43", "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected generated subtask IDs: got %v want %v", got, want)
	}
	if err := startrek.PostSplitCreatedComment(ctx, storage, "VAY-42", splitIssueIDs(splitResult.Issues)); err != nil {
		t.Fatalf("post split-created comment: %v", err)
	}

	arcRunner := &trackerWatchRecordingArcRunner{}
	loop := agentpkg.NewLoopWithTaskEngine(storage, enginepkg.NewTaskEngine(), runner, nil, agentpkg.LoopOptions{
		ParentID:       "VAY",
		MaxRetries:     0,
		Concurrency:    1,
		RepoRoot:       repoRoot,
		Backend:        "fake-codex",
		Model:          "fake-codex",
		VCS:            arcvcs.New(arcRunner),
		RequireReview:  true,
		MergeOnSuccess: true,
	})
	summary, err := loop.Run(ctx)
	if err != nil {
		t.Fatalf("run split subtasks: %v", err)
	}
	if summary.Completed != 2 {
		t.Fatalf("expected both generated subtasks to complete, got %#v", summary)
	}
	for _, subtaskID := range []string{"VAY-43", "VAY-44"} {
		task, err := storage.GetTask(ctx, subtaskID)
		if err != nil {
			t.Fatalf("get subtask %s: %v", subtaskID, err)
		}
		if task.Status != contracts.TaskStatusClosed {
			t.Fatalf("expected generated subtask %s closed, got %s", subtaskID, task.Status)
		}
	}

	if got := arcRunner.count("commit"); got != 2 {
		t.Fatalf("expected one Arc commit per generated subtask, got %d calls: %#v", got, arcRunner.calls)
	}
	if got := arcRunner.count("pr create"); got != 1 {
		t.Fatalf("expected exactly one parent Arc PR, got %d calls: %#v", got, arcRunner.calls)
	}
	parentComments := storage.commentTexts("VAY-42")
	prURLComments := matchingComments(parentComments, "https://a.yandex-team.ru/review/123456")
	if len(prURLComments) != 1 {
		t.Fatalf("expected exactly one parent PR URL comment, got %d comments:\n%s", len(prURLComments), strings.Join(parentComments, "\n---\n"))
	}
	if !strings.Contains(prURLComments[0], "<!-- yolo-runner:parent-pr-created -->") {
		t.Fatalf("expected parent PR comment marker, got:\n%s", prURLComments[0])
	}
}

type trackerWatchSplitPRRunner struct {
	mu       sync.Mutex
	requests []contracts.RunnerRequest
}

func (r *trackerWatchSplitPRRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, contracts.RunnerRequest{
		TaskID:   request.TaskID,
		ParentID: request.ParentID,
		Prompt:   request.Prompt,
		Mode:     request.Mode,
		Model:    request.Model,
		RepoRoot: request.RepoRoot,
		Metadata: cloneStringMapForTrackerWatchTest(request.Metadata),
	})
	r.mu.Unlock()

	switch {
	case strings.Contains(request.Prompt, "evaluating whether a queued task is actionable"):
		emitTrackerWatchRunnerOutput(request, `{"decision":"ready","confidence":0.95,"summary":"Ready to split.","questions":[]}`)
	case strings.Contains(request.Prompt, "Run the bundled strict task splitter"):
		emitTrackerWatchRunnerOutput(request, trackerWatchStrictSplitOutput())
	case request.Mode == contracts.RunnerModeReview:
		emitTrackerWatchRunnerOutput(request, "REVIEW_VERDICT: pass")
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted, ReviewReady: true}, nil
	case request.Mode == contracts.RunnerModeImplement:
		emitTrackerWatchRunnerOutput(request, "implementation complete")
	default:
		return contracts.RunnerResult{}, fmt.Errorf("unexpected fake runner request mode %q", request.Mode)
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func (r *trackerWatchSplitPRRunner) requestSnapshots() []contracts.RunnerRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contracts.RunnerRequest(nil), r.requests...)
}

func emitTrackerWatchRunnerOutput(request contracts.RunnerRequest, message string) {
	if request.OnProgress == nil {
		return
	}
	request.OnProgress(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeAgentText),
		Message:   message,
		Metadata:  map[string]string{"source": "stdout"},
		Timestamp: time.Now().UTC(),
	})
}

func trackerWatchStrictSplitOutput() string {
	payload, err := json.Marshal(splitter.StrictOutput{
		Epics: []splitter.Epic{
			{Name: "Split-to-PR integration", Goal: "Prove split subtasks land into one parent PR."},
		},
		Tasks: []splitter.Task{
			trackerWatchStrictTask("T36.1", "Implement first generated subtask", "Close the first leaf generated by strict split.", []string{"none"}, []string{"T36.2"}),
			trackerWatchStrictTask("T36.2", "Implement dependent generated subtask", "Close the second leaf only after the first subtask lands.", []string{"T36.1"}, []string{"none"}),
		},
		Order: []splitter.Dependency{
			{From: "T36.1", To: "T36.2"},
		},
		RiskNotes: []string{"Fake-backed integration must still exercise runner and VCS seams."},
	})
	if err != nil {
		panic(err)
	}
	return string(payload)
}

func trackerWatchStrictTask(id string, title string, why string, dependsOn []string, unlocks []string) splitter.Task {
	return splitter.Task{
		ID:         id,
		Title:      title,
		Why:        []string{why},
		InScope:    []string{"Exercise one generated subtask.", "Seam: split-to-PR integration harness"},
		OutOfScope: []string{"Operator docs."},
		StrictTDD: []string{
			"Add or update one targeted failing test first",
			"Run the targeted test and confirm it fails for the intended reason",
			"Implement the minimum production change needed to make it pass",
			"Re-run the targeted test",
			"Run one narrow follow-up verification command",
		},
		DoneWhen:      []string{"The generated subtask closes."},
		ExpectedFiles: []string{"cmd/yolo-agent/tracker_watch_integration_test.go"},
		DependsOn:     dependsOn,
		Unlocks:       unlocks,
	}
}

type trackerWatchSplitPRStorage struct {
	mu             sync.Mutex
	tasks          map[string]contracts.Task
	relations      []contracts.TaskRelation
	comments       map[string][]startrek.IssueComment
	nextIssueIndex int
}

func newTrackerWatchSplitPRStorage() *trackerWatchSplitPRStorage {
	return &trackerWatchSplitPRStorage{
		tasks: map[string]contracts.Task{
			"VAY": {
				ID:     "VAY",
				Title:  "VAY",
				Status: contracts.TaskStatusOpen,
			},
			"VAY-42": {
				ID:          "VAY-42",
				Title:       "Parent issue ready for splitting",
				Description: "Create generated subtasks and land them through Arc PR finalization.",
				Status:      contracts.TaskStatusOpen,
				ParentID:    "VAY",
				Metadata:    map[string]string{},
			},
		},
		relations: []contracts.TaskRelation{
			{FromID: "VAY", ToID: "VAY-42", Type: contracts.RelationParent},
		},
		comments:       map[string][]startrek.IssueComment{},
		nextIssueIndex: 43,
	}
}

func (s *trackerWatchSplitPRStorage) GetTaskTree(_ context.Context, rootID string) (*contracts.TaskTree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks := make(map[string]contracts.Task, len(s.tasks))
	for id, task := range s.tasks {
		tasks[id] = cloneTrackerWatchTask(task)
	}
	root, ok := tasks[strings.TrimSpace(rootID)]
	if !ok {
		return nil, fmt.Errorf("missing root task %q", rootID)
	}
	return &contracts.TaskTree{
		Root:      root,
		Tasks:     tasks,
		Relations: append([]contracts.TaskRelation(nil), s.relations...),
	}, nil
}

func (s *trackerWatchSplitPRStorage) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return nil, fmt.Errorf("missing task %q", taskID)
	}
	cloned := cloneTrackerWatchTask(task)
	return &cloned, nil
}

func (s *trackerWatchSplitPRStorage) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return fmt.Errorf("missing task %q", taskID)
	}
	task.Status = status
	s.tasks[task.ID] = task
	return nil
}

func (s *trackerWatchSplitPRStorage) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return fmt.Errorf("missing task %q", taskID)
	}
	if task.Metadata == nil {
		task.Metadata = map[string]string{}
	}
	for key, value := range data {
		task.Metadata[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	s.tasks[task.ID] = task
	return nil
}

func (s *trackerWatchSplitPRStorage) RemoveLabel(_ context.Context, taskID string, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return fmt.Errorf("missing task %q", taskID)
	}
	if status, ok := trackerWatchTestStatusForLabel(label); ok && task.Status == status {
		task.Status = contracts.TaskStatusOpen
		s.tasks[task.ID] = task
	}
	return nil
}

func (s *trackerWatchSplitPRStorage) AddLabel(_ context.Context, taskID string, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return fmt.Errorf("missing task %q", taskID)
	}
	if status, ok := trackerWatchTestStatusForLabel(label); ok {
		task.Status = status
		s.tasks[task.ID] = task
	}
	return nil
}

func trackerWatchTestStatusForLabel(label string) (contracts.TaskStatus, bool) {
	switch strings.TrimSpace(label) {
	case "yolo-agent-ready":
		return contracts.TaskStatusOpen, true
	case "yolo-agent-in-progress":
		return contracts.TaskStatusInProgress, true
	case "yolo-agent-completed":
		return contracts.TaskStatusClosed, true
	case "yolo-agent-blocked":
		return contracts.TaskStatusBlocked, true
	case "yolo-agent-failed":
		return contracts.TaskStatusFailed, true
	default:
		return "", false
	}
}

func (s *trackerWatchSplitPRStorage) CreateIssue(_ context.Context, opts startrek.IssueCreateOptions) (startrek.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	issueID := fmt.Sprintf("VAY-%d", s.nextIssueIndex)
	s.nextIssueIndex++
	task := contracts.Task{
		ID:          issueID,
		Title:       strings.TrimSpace(opts.Title),
		Description: strings.TrimSpace(opts.Description),
		Status:      contracts.TaskStatusOpen,
		ParentID:    strings.TrimSpace(opts.ParentID),
		Metadata:    map[string]string{},
	}
	s.tasks[issueID] = task
	s.relations = append(s.relations, contracts.TaskRelation{
		FromID: task.ParentID,
		ToID:   issueID,
		Type:   contracts.RelationParent,
	})
	return startrek.Issue{
		ID:          issueID,
		Title:       task.Title,
		Description: task.Description,
		Labels:      append([]string(nil), opts.Labels...),
		ParentID:    task.ParentID,
	}, nil
}

func (s *trackerWatchSplitPRStorage) CreateIssueLink(_ context.Context, opts startrek.IssueLinkCreateOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	issueID := strings.TrimSpace(opts.IssueID)
	relatedIssueID := strings.TrimSpace(opts.RelatedIssueID)
	if issueID == "" || relatedIssueID == "" {
		return nil
	}
	task := s.tasks[issueID]
	task.ID = issueID
	if task.Metadata == nil {
		task.Metadata = map[string]string{}
	}
	dependencies := appendDependencyID(task.Metadata["dependencies"], relatedIssueID)
	task.Metadata["dependencies"] = strings.Join(dependencies, ",")
	s.tasks[issueID] = task
	s.relations = append(s.relations,
		contracts.TaskRelation{FromID: issueID, ToID: relatedIssueID, Type: contracts.RelationDependsOn},
		contracts.TaskRelation{FromID: relatedIssueID, ToID: issueID, Type: contracts.RelationBlocks},
	)
	return nil
}

func (s *trackerWatchSplitPRStorage) GetIssueComments(_ context.Context, issueID string) ([]startrek.IssueComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]startrek.IssueComment(nil), s.comments[strings.TrimSpace(issueID)]...), nil
}

func (s *trackerWatchSplitPRStorage) CreateIssueComment(_ context.Context, issueID string, opts startrek.IssueCommentCreateOptions) (startrek.IssueComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issueID = strings.TrimSpace(issueID)
	body := strings.TrimSpace(opts.Body)
	if marker := strings.TrimSpace(opts.Marker); marker != "" {
		body = "<!-- yolo-runner:" + marker + " -->\n\n" + body
	}
	comment := startrek.IssueComment{
		ID:        fmt.Sprintf("comment-%d", len(s.comments[issueID])+1),
		Body:      body,
		Author:    startrek.IssueAuthor{ID: "runner", Display: "YOLO Runner"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	s.comments[issueID] = append(s.comments[issueID], comment)
	return comment, nil
}

func (s *trackerWatchSplitPRStorage) commentTexts(issueID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	comments := s.comments[strings.TrimSpace(issueID)]
	out := make([]string, 0, len(comments))
	for _, comment := range comments {
		out = append(out, strings.TrimSpace(comment.Body))
	}
	return out
}

type trackerWatchRecordingArcRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *trackerWatchRecordingArcRunner) Run(name string, args ...string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	r.mu.Unlock()

	if name != "arc" || len(args) == 0 {
		return "", fmt.Errorf("unexpected Arc command %s %v", name, args)
	}
	switch args[0] {
	case "checkout", "add", "commit", "status":
		return "", nil
	case "rev-parse":
		return "abc123\n", nil
	case "pr":
		if len(args) >= 2 && args[1] == "create" {
			return `{"url":"https://a.yandex-team.ru/review/123456"}` + "\n", nil
		}
	}
	return "", fmt.Errorf("unexpected Arc command arc %s", strings.Join(args, " "))
}

func (r *trackerWatchRecordingArcRunner) count(commandPrefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	prefix := strings.TrimSpace("arc " + commandPrefix)
	for _, call := range r.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

func splitTaskIDs(tasks []splitter.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, strings.TrimSpace(task.ID))
	}
	return ids
}

func splitIssueIDs(issues []startrek.Issue) []string {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, strings.TrimSpace(issue.ID))
	}
	return ids
}

func matchingComments(comments []string, needle string) []string {
	matches := make([]string, 0)
	for _, comment := range comments {
		if strings.Contains(comment, needle) {
			matches = append(matches, comment)
		}
	}
	return matches
}

func cloneTrackerWatchTask(task contracts.Task) contracts.Task {
	task.Metadata = cloneStringMapForTrackerWatchTest(task.Metadata)
	return task
}

func appendDependencyID(existing string, dependencyID string) []string {
	dependencyID = strings.TrimSpace(dependencyID)
	ids := make([]string, 0)
	seen := map[string]struct{}{}
	for _, raw := range strings.Split(existing, ",") {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		seen[strings.ToLower(id)] = struct{}{}
		ids = append(ids, id)
	}
	if dependencyID != "" {
		key := strings.ToLower(dependencyID)
		if _, ok := seen[key]; !ok {
			ids = append(ids, dependencyID)
		}
	}
	return ids
}

func cloneStringMapForTrackerWatchTest(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

type fakeTrackerWatchStartrek struct {
	*httptest.Server

	t        *testing.T
	mu       sync.Mutex
	issue    map[string]any
	comments []map[string]any
}

func newFakeTrackerWatchStartrek(t *testing.T) *fakeTrackerWatchStartrek {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "startrek", "testdata", "tracker_watch_ready_issue.json"))
	if err != nil {
		t.Fatalf("read Startrek fixture: %v", err)
	}
	var issue map[string]any
	if err := json.Unmarshal(raw, &issue); err != nil {
		t.Fatalf("decode Startrek fixture: %v", err)
	}

	fake := &fakeTrackerWatchStartrek{
		t:     t,
		issue: issue,
	}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.handle))
	return fake
}

func (f *fakeTrackerWatchStartrek) handle(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()
	if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "OAuth tracker-token" {
		f.t.Fatalf("expected Startrek OAuth token, got %q", got)
	}

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/issues/_search":
		f.handleSearch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/issues/VAY-42":
		f.writeJSON(w, http.StatusOK, f.issueSnapshot())
	case r.Method == http.MethodGet && r.URL.Path == "/issues/VAY-42/comments":
		f.writeJSON(w, http.StatusOK, f.commentsSnapshot())
	case r.Method == http.MethodPatch && r.URL.Path == "/issues/VAY-42":
		f.handleLabelPatch(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/issues/VAY-42/comments":
		f.handleCreateComment(w, r)
	default:
		f.t.Fatalf("unexpected Startrek request: %s %s", r.Method, r.URL.String())
	}
}

func (f *fakeTrackerWatchStartrek) handleSearch(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Filter map[string]any `json:"filter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode Startrek search: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprint(payload.Filter["queue"])); got != "VAY" {
		f.t.Fatalf("expected VAY queue search, got %q", got)
	}
	label := strings.TrimSpace(fmt.Sprint(payload.Filter["tags"]))

	f.mu.Lock()
	include := hasLabel(mapStringSlice(f.issue["tags"]), label)
	issue := cloneJSONMap(f.issue)
	f.mu.Unlock()

	w.Header().Set("X-Total-Pages", "1")
	if include {
		w.Header().Set("X-Total-Count", "1")
		f.writeJSON(w, http.StatusOK, []map[string]any{issue})
		return
	}
	w.Header().Set("X-Total-Count", "0")
	f.writeJSON(w, http.StatusOK, []map[string]any{})
}

func (f *fakeTrackerWatchStartrek) handleLabelPatch(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Tags map[string][]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode Startrek label patch: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	labels := mapStringSlice(f.issue["tags"])
	for _, label := range payload.Tags["remove"] {
		labels = removeLabel(labels, label)
	}
	for _, label := range payload.Tags["add"] {
		if !hasLabel(labels, label) {
			labels = append(labels, strings.TrimSpace(label))
		}
	}
	f.issue["tags"] = labels
	f.writeJSON(w, http.StatusOK, map[string]any{})
}

func (f *fakeTrackerWatchStartrek) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Text       string   `json:"text"`
		Summonees  []string `json:"summonees"`
		MarkupType string   `json:"markupType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode Startrek create comment: %v", err)
	}
	f.mu.Lock()
	createdAt := fmt.Sprintf("2026-05-28T05:%02d:00.000+0000", len(f.comments))
	comment := map[string]any{
		"id":   len(f.comments) + 1,
		"text": payload.Text,
		"createdBy": map[string]any{
			"id":      "runner",
			"display": "YOLO Runner",
		},
		"createdAt": createdAt,
		"updatedAt": createdAt,
	}

	f.comments = append(f.comments, comment)
	f.mu.Unlock()
	f.writeJSON(w, http.StatusCreated, comment)
}

func (f *fakeTrackerWatchStartrek) issueSnapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneJSONMap(f.issue)
}

func (f *fakeTrackerWatchStartrek) commentsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.comments))
	for _, comment := range f.comments {
		out = append(out, cloneJSONMap(comment))
	}
	return out
}

func (f *fakeTrackerWatchStartrek) labels(issueID string) []string {
	if issueID != "VAY-42" {
		f.t.Fatalf("unexpected issue ID %q", issueID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), mapStringSlice(f.issue["tags"])...)
}

func (f *fakeTrackerWatchStartrek) commentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.comments))
	for _, comment := range f.comments {
		out = append(out, strings.TrimSpace(fmt.Sprint(comment["text"])))
	}
	return out
}

func (f *fakeTrackerWatchStartrek) addComment(authorID string, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	createdAt := fmt.Sprintf("2026-05-28T05:%02d:00.000+0000", len(f.comments))
	f.comments = append(f.comments, map[string]any{
		"id":   len(f.comments) + 1,
		"text": text,
		"createdBy": map[string]any{
			"id":      authorID,
			"display": authorID,
		},
		"createdAt": createdAt,
		"updatedAt": createdAt,
	})
}

func (f *fakeTrackerWatchStartrek) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		f.t.Fatalf("encode Startrek response: %v", err)
	}
}

func countNeedsInfoComments(comments []string) int {
	count := 0
	for _, comment := range comments {
		if strings.Contains(comment, "<!-- yolo-runner:needs-info -->") {
			count++
		}
	}
	return count
}

func writeTrackerWatchFakeCodex(t *testing.T, repoRoot string) string {
	return writeTrackerWatchFakeCodexOutput(t, repoRoot, `{"decision":"needs_info","confidence":0.42,"summary":"Ownership is unclear.","questions":["Which package owns this behavior?","Who should answer follow-up questions?"]}`)
}

func writeTrackerWatchFakeCodexOutput(t *testing.T, repoRoot string, output string) string {
	t.Helper()
	path := filepath.Join(repoRoot, "fake-codex")
	script := strings.Join([]string{
		"#!/bin/sh",
		`printf . >> "$FAKE_CODEX_CALLS"`,
		`printf '%s\n' ` + strconv.Quote(output),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func writeTrackerWatchFakeCodexBackend(t *testing.T, repoRoot string, binary string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir coding agents dir: %v", err)
	}
	payload := fmt.Sprintf(`
name: codex-cli
type: command
backend: codex-cli
model: fake-codex
binary: %s
args:
  - "{{prompt}}"
adapter: command
supports_review: true
supports_stream: true
`, strconv.Quote(binary))
	if err := os.WriteFile(filepath.Join(dir, "codex-cli.yaml"), []byte(strings.TrimSpace(payload)+"\n"), 0o644); err != nil {
		t.Fatalf("write fake codex backend: %v", err)
	}
}

func fakeCodexCallCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read fake codex calls: %v", err)
	}
	return strings.Count(string(raw), ".")
}

func cloneJSONMap(in map[string]any) map[string]any {
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

func mapStringSlice(raw any) []string {
	values, ok := raw.([]any)
	if ok {
		out := make([]string, 0, len(values))
		for _, value := range values {
			if label := strings.TrimSpace(fmt.Sprint(value)); label != "" {
				out = append(out, label)
			}
		}
		return out
	}
	labels, ok := raw.([]string)
	if !ok {
		return nil
	}
	return append([]string(nil), labels...)
}

func hasLabel(labels []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, label := range labels {
		if strings.TrimSpace(label) == want {
			return true
		}
	}
	return false
}

func removeLabel(labels []string, remove string) []string {
	remove = strings.TrimSpace(remove)
	out := labels[:0]
	for _, label := range labels {
		if strings.TrimSpace(label) != remove {
			out = append(out, label)
		}
	}
	return out
}
