package runner

import (
	"io"
	"testing"
)

// TestRunnerUsesNewInterfaces tests that the runner core can work with the new interfaces
func TestRunnerUsesNewInterfaces(t *testing.T) {
	// Setup fake implementations
	tracker := NewFakeTaskTracker()
	agent := NewFakeCodingAgent()

	// Add a test task
	task := Task{
		ID:                 "test-1",
		Title:              "Test Task",
		Description:        "Test Description",
		AcceptanceCriteria: "Test Acceptance Criteria",
		Status:             "open",
		IssueType:          "task",
	}
	tracker.AddTask(task)

	// Setup runner options
	opts := RunOnceOptions{
		RepoRoot: "/tmp/test-repo",
		RootID:   "test-1",
		Model:    "test-model",
		DryRun:   true, // Use dry run to avoid actual execution
		Out:      io.Discard,
	}

	// Setup dependencies with new interfaces
	deps := RunOnceDeps{
		TaskTracker: tracker,
		CodingAgent: agent,
		Prompt:      promptBuilder{},
		Git:         &fakeGitClient{},
		Logger:      &fakeTestLogger{},
	}

	// This should work with the new interfaces
	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("RunOnce failed with new interfaces: %v", err)
	}

	if result != "dry_run" {
		t.Errorf("Expected result 'dry_run', got %s", result)
	}

	// Verify the task tracker was used
	// The task should have been retrieved
	retrieved, err := tracker.Show("test-1")
	if err != nil {
		t.Fatalf("Failed to retrieve task from tracker: %v", err)
	}

	if retrieved.ID != "test-1" {
		t.Errorf("Expected task ID 'test-1', got %s", retrieved.ID)
	}

	// In dry run mode, the coding agent should not be called
	runs := agent.GetRuns()
	if len(runs) != 0 {
		t.Errorf("Expected 0 agent runs in dry run mode, got %d", len(runs))
	}
}

// TestRunnerBackwardCompatibility tests that the runner still works with legacy interfaces
func TestRunnerBackwardCompatibility(t *testing.T) {
	// Setup fake legacy implementations
	beadsClient := &fakeBeadsClient{
		tasks: map[string]Bead{
			"test-1": {
				ID:                 "test-1",
				Title:              "Test Task",
				Description:        "Test Description",
				AcceptanceCriteria: "Test Acceptance Criteria",
				Status:             "open",
			},
		},
		issues: map[string]Issue{
			"test-1": {
				ID:        "test-1",
				IssueType: "task",
				Status:    "open",
			},
		},
	}

	openCodeRunner := &fakeOpenCodeRunner{}

	// Setup runner options
	opts := RunOnceOptions{
		RepoRoot: "/tmp/test-repo",
		RootID:   "test-1",
		Model:    "test-model",
		DryRun:   true,
		Out:      io.Discard,
	}

	// Setup dependencies with legacy interfaces
	deps := RunOnceDeps{
		Beads:    beadsClient,
		OpenCode: openCodeRunner,
		Prompt:   promptBuilder{},
		Git:      &fakeGitClient{},
		Logger:   &fakeTestLogger{},
	}

	// This should work with legacy interfaces
	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("RunOnce failed with legacy interfaces: %v", err)
	}

	if result != "dry_run" {
		t.Errorf("Expected result 'dry_run', got %s", result)
	}
}

// TestRunnerMixedInterfaces tests that the runner works when mixing new and legacy interfaces
func TestRunnerMixedInterfaces(t *testing.T) {
	// Setup new TaskTracker but legacy OpenCode
	tracker := NewFakeTaskTracker()

	task := Task{
		ID:                 "test-1",
		Title:              "Test Task",
		Description:        "Test Description",
		AcceptanceCriteria: "Test Acceptance Criteria",
		Status:             "open",
		IssueType:          "task",
	}
	tracker.AddTask(task)

	openCodeRunner := &fakeOpenCodeRunner{}

	opts := RunOnceOptions{
		RepoRoot: "/tmp/test-repo",
		RootID:   "test-1",
		Model:    "test-model",
		DryRun:   true,
		Out:      io.Discard,
	}

	// Mix new TaskTracker with legacy OpenCode
	deps := RunOnceDeps{
		TaskTracker: tracker,
		OpenCode:    openCodeRunner,
		Prompt:      promptBuilder{},
		Git:         &fakeGitClient{},
		Logger:      &fakeTestLogger{},
	}

	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("RunOnce failed with mixed interfaces: %v", err)
	}

	if result != "dry_run" {
		t.Errorf("Expected result 'dry_run', got %s", result)
	}
}

// Fake implementations for testing
type fakeBeadsClient struct {
	tasks  map[string]Bead
	issues map[string]Issue
}

func (f *fakeBeadsClient) Ready(rootID string) (Issue, error) {
	if issue, exists := f.issues[rootID]; exists {
		return issue, nil
	}
	return Issue{}, &TaskNotFoundError{ID: rootID}
}

func (f *fakeBeadsClient) Tree(rootID string) (Issue, error) {
	return f.Ready(rootID)
}

func (f *fakeBeadsClient) Show(id string) (Bead, error) {
	if bead, exists := f.tasks[id]; exists {
		return bead, nil
	}
	return Bead{}, &TaskNotFoundError{ID: id}
}

func (f *fakeBeadsClient) UpdateStatus(id string, status string) error {
	if bead, exists := f.tasks[id]; exists {
		bead.Status = status
		f.tasks[id] = bead
	}
	if issue, exists := f.issues[id]; exists {
		issue.Status = status
		f.issues[id] = issue
	}
	return nil
}

func (f *fakeBeadsClient) UpdateStatusWithReason(id string, status string, reason string) error {
	return f.UpdateStatus(id, status)
}

func (f *fakeBeadsClient) Close(id string) error {
	return f.UpdateStatus(id, "closed")
}

func (f *fakeBeadsClient) CloseEligible() error {
	return nil
}

func (f *fakeBeadsClient) Sync() error {
	return nil
}

type fakeOpenCodeRunner struct{}

func (f *fakeOpenCodeRunner) Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	return nil
}

type fakeGitClient struct{}

func (f *fakeGitClient) AddAll() error {
	return nil
}

func (f *fakeGitClient) IsDirty() (bool, error) {
	return false, nil
}

func (f *fakeGitClient) Commit(message string) error {
	return nil
}

func (f *fakeGitClient) RevParseHead() (string, error) {
	return "abc123", nil
}

type fakeTestLogger struct{}

func (f *fakeTestLogger) AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error {
	return nil
}

type promptBuilder struct{}

func (p promptBuilder) Build(issueID string, title string, description string, acceptance string) string {
	return "Test prompt for " + title
}
