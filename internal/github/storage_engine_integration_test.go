package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/engine"
)

func TestStorageBackendAndTaskEngineEpic52RunsOnlyEightSubTasks(t *testing.T) {
	ctx := context.Background()

	parent52 := intPtr(52)
	parent53 := intPtr(53)
	fixture := newStorageEngineFixture(t, []githubIssuePayload{
		newIssuePayload(52, "Epic 52", "open", nil, ""),
		newIssuePayload(58, "Task 58", "open", parent52, ""),
		newIssuePayload(59, "Task 59", "open", parent52, ""),
		newIssuePayload(60, "Task 60", "open", parent52, ""),
		newIssuePayload(61, "Task 61", "open", parent52, ""),
		newIssuePayload(62, "Task 62", "open", parent52, ""),
		newIssuePayload(63, "Task 63", "open", parent52, ""),
		newIssuePayload(64, "Task 64", "open", parent52, ""),
		newIssuePayload(65, "Task 65", "open", parent52, ""),
		newIssuePayload(53, "Unrelated Epic", "open", nil, ""),
		newIssuePayload(90, "Unrelated Child", "open", parent53, ""),
		newPullRequestPayload(91, "Automation PR"),
	})

	taskEngine := engine.NewTaskEngine()
	tree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	if len(graph.Nodes) != 9 {
		t.Fatalf("expected 9 nodes (root + 8 subtasks), got %d", len(graph.Nodes))
	}
	if _, ok := graph.Nodes["53"]; ok {
		t.Fatalf("unrelated epic 53 must not be present in graph")
	}
	if _, ok := graph.Nodes["90"]; ok {
		t.Fatalf("unrelated child 90 must not be present in graph")
	}

	gotRunnable := sortedSummaryIDs(taskEngine.GetNextAvailable(graph))
	wantRunnable := []string{"58", "59", "60", "61", "62", "63", "64", "65"}
	if !reflect.DeepEqual(gotRunnable, wantRunnable) {
		t.Fatalf("expected runnable subtasks %v, got %v", wantRunnable, gotRunnable)
	}
}

func TestStorageBackendAndTaskEngineWorkflowRespectsDependencyOrder(t *testing.T) {
	ctx := context.Background()

	parent52 := intPtr(52)
	fixture := newStorageEngineFixture(t, []githubIssuePayload{
		newIssuePayload(52, "Epic 52", "open", nil, ""),
		newIssuePayload(58, "Task 58", "open", parent52, ""),
		newIssuePayload(59, "Task 59", "open", parent52, "depends-on:#58"),
		newIssuePayload(60, "Task 60", "open", parent52, "depends-on:#58"),
		newIssuePayload(61, "Task 61", "open", parent52, "depends-on:#59,#60"),
		newIssuePayload(62, "Task 62", "open", parent52, ""),
		newIssuePayload(63, "Task 63", "open", parent52, "depends-on:#62"),
		newIssuePayload(64, "Task 64", "open", parent52, "depends-on:#61,#63"),
		newIssuePayload(65, "Task 65", "open", parent52, "depends-on:#64"),
	})

	taskEngine := engine.NewTaskEngine()
	tree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	processed := executeWorkflowToNoRemainingRunnableTasks(t, ctx, fixture.backend, taskEngine, graph)
	wantCount := len(graph.Nodes) - 1
	if len(processed) != wantCount {
		t.Fatalf("expected %d processed subtasks, got %d (%v)", wantCount, len(processed), processed)
	}
	assertDependencyOrder(t, graph, processed)
}

func TestStorageBackendAndTaskEngineConcurrentStatusUpdatesReflectInGraph(t *testing.T) {
	ctx := context.Background()

	parent52 := intPtr(52)
	fixture := newStorageEngineFixture(t, []githubIssuePayload{
		newIssuePayload(52, "Epic 52", "open", nil, ""),
		newIssuePayload(58, "Task 58", "open", parent52, ""),
		newIssuePayload(59, "Task 59", "open", parent52, "depends-on:#58"),
		newIssuePayload(60, "Task 60", "open", parent52, "depends-on:#58"),
		newIssuePayload(61, "Task 61", "open", parent52, "depends-on:#59,#60"),
	})

	taskEngine := engine.NewTaskEngine()
	tree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	if got := sortedSummaryIDs(taskEngine.GetNextAvailable(graph)); !reflect.DeepEqual(got, []string{"58"}) {
		t.Fatalf("initial runnable tasks = %v, want [58]", got)
	}
	if err := setTaskStatusInStorageAndGraph(ctx, fixture.backend, taskEngine, graph, "58", contracts.TaskStatusClosed); err != nil {
		t.Fatalf("close task 58: %v", err)
	}

	if got := sortedSummaryIDs(taskEngine.GetNextAvailable(graph)); !reflect.DeepEqual(got, []string{"59", "60"}) {
		t.Fatalf("runnable tasks after closing 58 = %v, want [59 60]", got)
	}

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, taskID := range []string{"59", "60"} {
		taskID := taskID
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- setTaskStatusInStorageAndGraph(ctx, fixture.backend, taskEngine, graph, taskID, contracts.TaskStatusClosed)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent status update failed: %v", err)
		}
	}

	if got := sortedSummaryIDs(taskEngine.GetNextAvailable(graph)); !reflect.DeepEqual(got, []string{"61"}) {
		t.Fatalf("runnable tasks after concurrent updates = %v, want [61]", got)
	}
}

func TestStorageBackendAndTaskEngineRefreshPicksUpExternalStatusChanges(t *testing.T) {
	ctx := context.Background()

	parent52 := intPtr(52)
	fixture := newStorageEngineFixture(t, []githubIssuePayload{
		newIssuePayload(52, "Epic 52", "open", nil, ""),
		newIssuePayload(58, "Task 58", "open", parent52, ""),
		newIssuePayload(59, "Task 59", "open", parent52, "depends-on:#58"),
	})

	taskEngine := engine.NewTaskEngine()
	tree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}
	if got := sortedSummaryIDs(taskEngine.GetNextAvailable(graph)); !reflect.DeepEqual(got, []string{"58"}) {
		t.Fatalf("initial runnable tasks = %v, want [58]", got)
	}

	fixture.setIssueState(58, "closed")

	refreshedTree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree (refresh) returned error: %v", err)
	}
	refreshedGraph, err := taskEngine.BuildGraph(refreshedTree)
	if err != nil {
		t.Fatalf("BuildGraph (refresh) returned error: %v", err)
	}

	if got := refreshedGraph.Nodes["58"].Status; got != contracts.TaskStatusClosed {
		t.Fatalf("refreshed status for task 58 = %q, want %q", got, contracts.TaskStatusClosed)
	}
	if got := sortedSummaryIDs(taskEngine.GetNextAvailable(refreshedGraph)); !reflect.DeepEqual(got, []string{"59"}) {
		t.Fatalf("runnable tasks after refresh = %v, want [59]", got)
	}
}

func TestStorageBackendAndTaskEngineEndToEndFiftyTasksCompletes(t *testing.T) {
	ctx := context.Background()
	fixture := newStorageEngineFixture(t, buildLayeredIssueSet(52, 50, 5))

	taskEngine := engine.NewTaskEngine()
	tree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}
	if len(graph.Nodes) != 51 {
		t.Fatalf("expected 51 nodes (root + 50 tasks), got %d", len(graph.Nodes))
	}

	processed := executeWorkflowToNoRemainingRunnableTasks(t, ctx, fixture.backend, taskEngine, graph)
	if len(processed) != 50 {
		t.Fatalf("expected 50 processed tasks, got %d", len(processed))
	}
	assertDependencyOrder(t, graph, processed)

	if err := setTaskStatusInStorageAndGraph(ctx, fixture.backend, taskEngine, graph, graph.RootID, contracts.TaskStatusClosed); err != nil {
		t.Fatalf("close root task: %v", err)
	}
	if !taskEngine.IsComplete(graph) {
		t.Fatalf("expected graph to be complete after processing 50 tasks and closing root")
	}
}

func TestStorageBackendAndTaskEngineEndToEndOneHundredFiftyTasksCompletes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	fixture := newStorageEngineFixture(t, buildLayeredIssueSet(52, 150, 10))
	taskEngine := engine.NewTaskEngine()

	tree, err := fixture.backend.GetTaskTree(ctx, "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}
	if len(graph.Nodes) != 151 {
		t.Fatalf("expected 151 nodes (root + 150 tasks), got %d", len(graph.Nodes))
	}

	processed := executeWorkflowToNoRemainingRunnableTasks(t, ctx, fixture.backend, taskEngine, graph)
	if len(processed) != 150 {
		t.Fatalf("expected 150 processed tasks, got %d", len(processed))
	}
	assertDependencyOrder(t, graph, processed)

	if err := setTaskStatusInStorageAndGraph(ctx, fixture.backend, taskEngine, graph, graph.RootID, contracts.TaskStatusClosed); err != nil {
		t.Fatalf("close root task: %v", err)
	}
	if !taskEngine.IsComplete(graph) {
		t.Fatalf("expected graph to be complete after processing 150 tasks and closing root")
	}
}

func executeWorkflowToNoRemainingRunnableTasks(
	t *testing.T,
	ctx context.Context,
	backend contracts.StorageBackend,
	taskEngine contracts.TaskEngine,
	graph *contracts.TaskGraph,
) []string {
	t.Helper()

	processed := make([]string, 0, len(graph.Nodes))
	maxIterations := len(graph.Nodes) * 3
	for i := 0; i < maxIterations; i++ {
		available := taskEngine.GetNextAvailable(graph)
		if len(available) == 0 {
			return processed
		}
		if summaryListContainsID(available, graph.RootID) {
			t.Fatalf("root task %q must not be runnable when executing a task tree", graph.RootID)
		}
		for _, summary := range available {
			if err := setTaskStatusInStorageAndGraph(ctx, backend, taskEngine, graph, summary.ID, contracts.TaskStatusClosed); err != nil {
				t.Fatalf("close task %q: %v", summary.ID, err)
			}
			processed = append(processed, summary.ID)
		}
	}

	t.Fatalf("workflow did not converge after %d iterations", maxIterations)
	return nil
}

func setTaskStatusInStorageAndGraph(
	ctx context.Context,
	backend contracts.StorageBackend,
	taskEngine contracts.TaskEngine,
	graph *contracts.TaskGraph,
	taskID string,
	status contracts.TaskStatus,
) error {
	if err := backend.SetTaskStatus(ctx, taskID, status); err != nil {
		return err
	}
	return taskEngine.UpdateTaskStatus(graph, taskID, status)
}

func assertDependencyOrder(t *testing.T, graph *contracts.TaskGraph, processed []string) {
	t.Helper()

	indexByID := make(map[string]int, len(processed))
	for idx, id := range processed {
		if _, exists := indexByID[id]; exists {
			t.Fatalf("task %q processed more than once", id)
		}
		indexByID[id] = idx
	}

	for _, edge := range graph.Edges {
		if edge.Type != contracts.RelationDependsOn {
			continue
		}
		dependencyIndex, dependencySeen := indexByID[edge.ToID]
		taskIndex, taskSeen := indexByID[edge.FromID]
		if !dependencySeen || !taskSeen {
			t.Fatalf("missing processed dependency edge %s -> %s in order %v", edge.FromID, edge.ToID, processed)
		}
		if dependencyIndex > taskIndex {
			t.Fatalf("dependency order violated: %s ran at %d before dependency %s at %d", edge.FromID, taskIndex, edge.ToID, dependencyIndex)
		}
	}
}

func sortedSummaryIDs(tasks []contracts.TaskSummary) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)
	return ids
}

func summaryListContainsID(tasks []contracts.TaskSummary, taskID string) bool {
	for _, task := range tasks {
		if task.ID == taskID {
			return true
		}
	}
	return false
}

func buildLayeredIssueSet(rootNumber int, totalTasks int, width int) []githubIssuePayload {
	if totalTasks <= 0 {
		return []githubIssuePayload{newIssuePayload(rootNumber, fmt.Sprintf("Epic %d", rootNumber), "open", nil, "")}
	}
	if width <= 0 {
		width = 1
	}

	issues := make([]githubIssuePayload, 0, totalTasks+1)
	issues = append(issues, newIssuePayload(rootNumber, fmt.Sprintf("Epic %d", rootNumber), "open", nil, ""))

	parent := intPtr(rootNumber)
	base := 100
	for i := 0; i < totalTasks; i++ {
		number := base + i
		body := ""
		if i >= width {
			dependencyNumber := base + (i - width)
			body = fmt.Sprintf("depends-on:#%d", dependencyNumber)
		}
		issues = append(issues, newIssuePayload(number, fmt.Sprintf("Task %d", number), "open", parent, body))
	}
	return issues
}

func newIssuePayload(number int, title string, state string, parentIssueID *int, body string) githubIssuePayload {
	payload := githubIssuePayload{
		Number: number,
		Title:  title,
		State:  state,
		Body:   body,
	}
	if parentIssueID != nil {
		parent := *parentIssueID
		payload.ParentIssueID = &parent
	}
	return payload
}

func newPullRequestPayload(number int, title string) githubIssuePayload {
	return githubIssuePayload{
		Number: number,
		Title:  title,
		State:  "open",
		PullRequest: &struct {
			URL string `json:"url"`
		}{
			URL: "https://api.github.com/repos/anomalyco/yolo-runner/pulls/" + strconv.Itoa(number),
		},
	}
}

func intPtr(value int) *int {
	v := value
	return &v
}

type storageEngineFixture struct {
	t       *testing.T
	server  *httptest.Server
	backend *StorageBackend

	mu     sync.Mutex
	issues map[int]githubIssuePayload
}

func newStorageEngineFixture(t *testing.T, issues []githubIssuePayload) *storageEngineFixture {
	t.Helper()

	fixture := &storageEngineFixture{
		t:      t,
		issues: make(map[int]githubIssuePayload, len(issues)),
	}
	for _, issue := range issues {
		fixture.issues[issue.Number] = cloneIssuePayload(issue)
	}

	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.handleRequest))
	t.Cleanup(fixture.server.Close)

	backend, err := NewStorageBackend(Config{
		Owner:       "anomalyco",
		Repo:        "yolo-runner",
		Token:       "ghp_test",
		APIEndpoint: fixture.server.URL,
		HTTPClient:  fixture.server.Client(),
	})
	if err != nil {
		t.Fatalf("build storage backend: %v", err)
	}
	fixture.backend = backend
	return fixture
}

func (f *storageEngineFixture) setIssueState(number int, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	issue, ok := f.issues[number]
	if !ok {
		f.t.Fatalf("cannot set state for missing issue %d", number)
	}
	issue.State = state
	f.issues[number] = issue
}

func (f *storageEngineFixture) handleRequest(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()

	if r.URL.Path == "/repos/anomalyco/yolo-runner" && r.Method == http.MethodGet {
		_, _ = w.Write([]byte(`{"full_name":"anomalyco/yolo-runner"}`))
		return
	}

	if r.URL.Path == "/repos/anomalyco/yolo-runner/issues" && r.Method == http.MethodGet {
		f.writeIssueListResponse(w, r)
		return
	}

	const issuePrefix = "/repos/anomalyco/yolo-runner/issues/"
	if !strings.HasPrefix(r.URL.Path, issuePrefix) {
		f.t.Fatalf("unexpected request path %q", r.URL.Path)
	}

	issueTail := strings.TrimPrefix(r.URL.Path, issuePrefix)
	if strings.HasSuffix(issueTail, "/comments") {
		f.handleIssueCommentsRequest(w, r, strings.TrimSuffix(issueTail, "/comments"))
		return
	}

	issueNumber, err := strconv.Atoi(issueTail)
	if err != nil {
		f.t.Fatalf("unexpected issue path %q", r.URL.Path)
	}

	switch r.Method {
	case http.MethodGet:
		f.handleIssueGetRequest(w, issueNumber)
	case http.MethodPatch:
		f.handleIssuePatchRequest(w, r, issueNumber)
	default:
		f.t.Fatalf("unsupported method %s for path %q", r.Method, r.URL.Path)
	}
}

func (f *storageEngineFixture) writeIssueListResponse(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	issues := make([]githubIssuePayload, 0, len(f.issues))
	for _, issue := range f.issues {
		issues = append(issues, cloneIssuePayload(issue))
	}
	sort.Slice(issues, func(i int, j int) bool {
		return issues[i].Number < issues[j].Number
	})

	page, ok := parsePositiveInt(r.URL.Query().Get("page"))
	if !ok {
		page = 1
	}
	perPage, ok := parsePositiveInt(r.URL.Query().Get("per_page"))
	if !ok {
		perPage = issuesPerPage
	}
	start := (page - 1) * perPage
	if start < 0 || start >= len(issues) {
		issues = []githubIssuePayload{}
	} else {
		end := start + perPage
		if end > len(issues) {
			end = len(issues)
		}
		issues = issues[start:end]
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(issues); err != nil {
		f.t.Fatalf("encode issue list: %v", err)
	}
}

func (f *storageEngineFixture) handleIssueGetRequest(w http.ResponseWriter, issueNumber int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	issue, ok := f.issues[issueNumber]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cloneIssuePayload(issue)); err != nil {
		f.t.Fatalf("encode issue %d: %v", issueNumber, err)
	}
}

func (f *storageEngineFixture) handleIssuePatchRequest(w http.ResponseWriter, r *http.Request, issueNumber int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	issue, ok := f.issues[issueNumber]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}

	var payload struct {
		State string `json:"state"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Fatalf("read patch body: %v", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		f.t.Fatalf("decode patch body: %v", err)
	}

	issue.State = strings.TrimSpace(payload.State)
	f.issues[issueNumber] = issue

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cloneIssuePayload(issue)); err != nil {
		f.t.Fatalf("encode patched issue %d: %v", issueNumber, err)
	}
}

func (f *storageEngineFixture) handleIssueCommentsRequest(w http.ResponseWriter, r *http.Request, issueID string) {
	f.t.Helper()
	if r.Method != http.MethodPost {
		f.t.Fatalf("unsupported method %s for comments endpoint", r.Method)
	}
	if _, err := strconv.Atoi(issueID); err != nil {
		f.t.Fatalf("invalid issue comments path segment %q", issueID)
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id":1}`))
}

func cloneIssuePayload(issue githubIssuePayload) githubIssuePayload {
	cloned := issue
	if issue.ParentIssueID != nil {
		parent := *issue.ParentIssueID
		cloned.ParentIssueID = &parent
	}
	if len(issue.Labels) > 0 {
		cloned.Labels = make([]githubLabelPayload, len(issue.Labels))
		copy(cloned.Labels, issue.Labels)
	}
	if issue.PullRequest != nil {
		cloned.PullRequest = &struct {
			URL string `json:"url"`
		}{
			URL: issue.PullRequest.URL,
		}
	}
	return cloned
}
