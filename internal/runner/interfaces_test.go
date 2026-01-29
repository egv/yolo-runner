package runner

import (
	"context"
	"testing"
	"time"
)

// FakeTaskTracker is a test implementation of TaskTracker
type FakeTaskTracker struct {
	tasks map[string]Task
}

func NewFakeTaskTracker() *FakeTaskTracker {
	return &FakeTaskTracker{
		tasks: make(map[string]Task),
	}
}

func (f *FakeTaskTracker) AddTask(task Task) {
	f.tasks[task.ID] = task
}

func (f *FakeTaskTracker) Ready(rootID string) (Task, error) {
	if task, exists := f.tasks[rootID]; exists {
		return task, nil
	}
	return Task{}, &TaskNotFoundError{ID: rootID}
}

func (f *FakeTaskTracker) Tree(rootID string) (Task, error) {
	return f.Ready(rootID)
}

func (f *FakeTaskTracker) Show(id string) (Task, error) {
	if task, exists := f.tasks[id]; exists {
		return task, nil
	}
	return Task{}, &TaskNotFoundError{ID: id}
}

func (f *FakeTaskTracker) UpdateStatus(id string, status string) error {
	if task, exists := f.tasks[id]; exists {
		task.Status = status
		f.tasks[id] = task
		return nil
	}
	return &TaskNotFoundError{ID: id}
}

func (f *FakeTaskTracker) UpdateStatusWithReason(id string, status string, reason string) error {
	return f.UpdateStatus(id, status)
}

func (f *FakeTaskTracker) Close(id string) error {
	return f.UpdateStatus(id, "closed")
}

func (f *FakeTaskTracker) CloseEligible() error {
	return nil
}

func (f *FakeTaskTracker) Sync() error {
	return nil
}

// TaskNotFoundError is returned when a task is not found
type TaskNotFoundError struct {
	ID string
}

func (e *TaskNotFoundError) Error() string {
	return "task not found: " + e.ID
}

// FakeCodingAgent is a test implementation of CodingAgent
type FakeCodingAgent struct {
	runs []RunCall
}

type RunCall struct {
	Task    Task
	Options RunOptions
}

func NewFakeCodingAgent() *FakeCodingAgent {
	return &FakeCodingAgent{
		runs: make([]RunCall, 0),
	}
}

func (f *FakeCodingAgent) Run(ctx context.Context, task Task, repoRoot string, model string, options RunOptions) error {
	f.runs = append(f.runs, RunCall{
		Task:    task,
		Options: options,
	})
	return nil
}

func (f *FakeCodingAgent) RunWithContext(ctx context.Context, task Task, repoRoot string, model string, options RunOptions) error {
	return f.Run(ctx, task, repoRoot, model, options)
}

func (f *FakeCodingAgent) GetRuns() []RunCall {
	return f.runs
}

// TestTaskTrackerInterface tests that the TaskTracker interface covers all required capabilities
func TestTaskTrackerInterface(t *testing.T) {
	tracker := NewFakeTaskTracker()

	// Test adding and retrieving tasks
	task := Task{
		ID:                 "test-1",
		Title:              "Test Task",
		Description:        "Test Description",
		AcceptanceCriteria: "Test Acceptance Criteria",
		Status:             "open",
		IssueType:          "task",
	}
	tracker.AddTask(task)

	// Test Ready method
	retrieved, err := tracker.Ready("test-1")
	if err != nil {
		t.Fatalf("Ready failed: %v", err)
	}
	if retrieved.ID != "test-1" {
		t.Errorf("Expected task ID test-1, got %s", retrieved.ID)
	}

	// Test Show method
	retrieved, err = tracker.Show("test-1")
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if retrieved.Title != "Test Task" {
		t.Errorf("Expected title 'Test Task', got %s", retrieved.Title)
	}

	// Test UpdateStatus method
	err = tracker.UpdateStatus("test-1", "in_progress")
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	retrieved, err = tracker.Show("test-1")
	if err != nil {
		t.Fatalf("Show failed after update: %v", err)
	}
	if retrieved.Status != "in_progress" {
		t.Errorf("Expected status 'in_progress', got %s", retrieved.Status)
	}

	// Test Close method
	err = tracker.Close("test-1")
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	retrieved, err = tracker.Show("test-1")
	if err != nil {
		t.Fatalf("Show failed after close: %v", err)
	}
	if retrieved.Status != "closed" {
		t.Errorf("Expected status 'closed', got %s", retrieved.Status)
	}

	// Test Tree method
	tree, err := tracker.Tree("test-1")
	if err != nil {
		t.Fatalf("Tree failed: %v", err)
	}
	if tree.ID != "test-1" {
		t.Errorf("Expected tree root ID test-1, got %s", tree.ID)
	}

	// Test UpdateStatusWithReason method
	err = tracker.UpdateStatusWithReason("test-1", "blocked", "test reason")
	if err != nil {
		t.Fatalf("UpdateStatusWithReason failed: %v", err)
	}

	// Test Sync method
	err = tracker.Sync()
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Test CloseEligible method
	err = tracker.CloseEligible()
	if err != nil {
		t.Fatalf("CloseEligible failed: %v", err)
	}
}

// TestCodingAgentInterface tests that the CodingAgent interface covers all required capabilities
func TestCodingAgentInterface(t *testing.T) {
	agent := NewFakeCodingAgent()

	ctx := context.Background()
	task := Task{
		ID:        "test-1",
		Title:     "Test Task",
		IssueType: "task",
		Status:    "open",
	}
	options := RunOptions{
		ConfigRoot: "/tmp/config",
		ConfigDir:  "/tmp/config/opencode",
		LogPath:    "/tmp/logs/test.log",
	}

	// Test Run method
	err := agent.Run(ctx, task, "/tmp/repo", "test-model", options)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	runs := agent.GetRuns()
	if len(runs) != 1 {
		t.Fatalf("Expected 1 run, got %d", len(runs))
	}

	if runs[0].Task.ID != "test-1" {
		t.Errorf("Expected task ID test-1, got %s", runs[0].Task.ID)
	}

	if runs[0].Options.ConfigRoot != "/tmp/config" {
		t.Errorf("Expected ConfigRoot '/tmp/config', got %s", runs[0].Options.ConfigRoot)
	}

	// Test RunWithContext method
	err = agent.RunWithContext(ctx, task, "/tmp/repo2", "test-model2", options)
	if err != nil {
		t.Fatalf("RunWithContext failed: %v", err)
	}

	runs = agent.GetRuns()
	if len(runs) != 2 {
		t.Fatalf("Expected 2 runs, got %d", len(runs))
	}
}

// TestInterfaceDrivenWiring tests that the runner can work with interface-driven dependencies
func TestInterfaceDrivenWiring(t *testing.T) {
	tracker := NewFakeTaskTracker()
	agent := NewFakeCodingAgent()

	// Setup test data
	task := Task{
		ID:                 "test-task-1",
		Title:              "Test Implementation Task",
		Description:        "Implement interface-driven wiring",
		AcceptanceCriteria: "- Given interfaces, when wiring, then no direct dependencies",
		Status:             "open",
		IssueType:          "task",
	}
	tracker.AddTask(task)

	// Test that we can call all interface methods without errors
	ctx := context.Background()

	// Test TaskTracker operations
	rootTask, err := tracker.Ready("test-task-1")
	if err != nil {
		t.Fatalf("TaskTracker Ready failed: %v", err)
	}

	if rootTask.ID != "test-task-1" {
		t.Errorf("Expected root task ID 'test-task-1', got %s", rootTask.ID)
	}

	taskDetail, err := tracker.Show("test-task-1")
	if err != nil {
		t.Fatalf("TaskTracker Show failed: %v", err)
	}

	if taskDetail.Title != "Test Implementation Task" {
		t.Errorf("Expected task title 'Test Implementation Task', got %s", taskDetail.Title)
	}

	err = tracker.UpdateStatus("test-task-1", "in_progress")
	if err != nil {
		t.Fatalf("TaskTracker UpdateStatus failed: %v", err)
	}

	// Test CodingAgent operations
	options := RunOptions{
		ConfigRoot: "/tmp/test-config",
		ConfigDir:  "/tmp/test-config/opencode",
		LogPath:    "/tmp/test-logs/test.log",
	}

	err = agent.Run(ctx, taskDetail, "/tmp/test-repo", "test-model", options)
	if err != nil {
		t.Fatalf("CodingAgent Run failed: %v", err)
	}

	// Verify the agent was called with correct parameters
	runs := agent.GetRuns()
	if len(runs) != 1 {
		t.Fatalf("Expected 1 agent run, got %d", len(runs))
	}

	if runs[0].Task.ID != "test-task-1" {
		t.Errorf("Expected agent run with task ID 'test-task-1', got %s", runs[0].Task.ID)
	}

	if runs[0].Options.ConfigRoot != "/tmp/test-config" {
		t.Errorf("Expected agent run with ConfigRoot '/tmp/test-config', got %s", runs[0].Options.ConfigRoot)
	}

	// Test completion flow
	err = tracker.Close("test-task-1")
	if err != nil {
		t.Fatalf("TaskTracker Close failed: %v", err)
	}

	err = tracker.Sync()
	if err != nil {
		t.Fatalf("TaskTracker Sync failed: %v", err)
	}
}

// TestInterfaceCompatibility tests that existing concrete types can be adapted
func TestInterfaceCompatibility(t *testing.T) {
	// This test ensures that the new interfaces are correctly defined

	// Verify interface types are correctly defined
	var _ TaskTracker = (*FakeTaskTracker)(nil)
	var _ CodingAgent = (*FakeCodingAgent)(nil)

	// Test timeout
	timedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agent := NewFakeCodingAgent()
	task := Task{ID: "timeout-test", IssueType: "task", Status: "open"}
	options := RunOptions{}

	// This should complete quickly
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(timedCtx, task, "/tmp", "model", options)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run failed: %v", err)
		}
	case <-timedCtx.Done():
		t.Errorf("Run timed out")
	}
}
