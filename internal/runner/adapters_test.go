package runner

import (
	"testing"
)

// TestTaskTrackerAdapter tests that TaskTrackerAdapter correctly adapts TaskTracker to BeadsClient
func TestTaskTrackerAdapter(t *testing.T) {
	tracker := NewFakeTaskTracker()

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

	// Test adapter
	adapter := NewTaskTrackerAdapter(tracker)
	if adapter == nil {
		t.Fatal("NewTaskTrackerAdapter returned nil")
	}

	// Test Ready method
	ready, err := adapter.Ready("test-1")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter Ready failed: %v", err)
	}
	if ready.ID != "test-1" {
		t.Errorf("Expected task ID test-1, got %s", ready.ID)
	}

	// Test Show method
	bead, err := adapter.Show("test-1")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter Show failed: %v", err)
	}
	if bead.Title != "Test Task" {
		t.Errorf("Expected title 'Test Task', got %s", bead.Title)
	}

	// Test Tree method
	tree, err := adapter.Tree("test-1")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter Tree failed: %v", err)
	}
	if tree.ID != "test-1" {
		t.Errorf("Expected tree ID test-1, got %s", tree.ID)
	}

	// Test UpdateStatus method
	err = adapter.UpdateStatus("test-1", "in_progress")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter UpdateStatus failed: %v", err)
	}

	// Verify update worked
	updated, err := adapter.Show("test-1")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter Show after update failed: %v", err)
	}
	if updated.Status != "in_progress" {
		t.Errorf("Expected status 'in_progress', got %s", updated.Status)
	}

	// Test UpdateStatusWithReason method
	err = adapter.UpdateStatusWithReason("test-1", "blocked", "test reason")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter UpdateStatusWithReason failed: %v", err)
	}

	// Test Close method
	err = adapter.Close("test-1")
	if err != nil {
		t.Fatalf("TaskTrackerAdapter Close failed: %v", err)
	}

	// Test Sync method
	err = adapter.Sync()
	if err != nil {
		t.Fatalf("TaskTrackerAdapter Sync failed: %v", err)
	}

	// Test CloseEligible method
	err = adapter.CloseEligible()
	if err != nil {
		t.Fatalf("TaskTrackerAdapter CloseEligible failed: %v", err)
	}
}

// TestCodingAgentAdapter tests that CodingAgentAdapter correctly adapts CodingAgent to OpenCodeRunner
func TestCodingAgentAdapter(t *testing.T) {
	agent := NewFakeCodingAgent()

	// Test adapter
	adapter := NewCodingAgentAdapter(agent)
	if adapter == nil {
		t.Fatal("NewCodingAgentAdapter returned nil")
	}

	// Test Run method
	err := adapter.Run("test-1", "/tmp/test-repo", "Implement feature X", "test-model", "/tmp/config", "/tmp/config/opencode", "/tmp/test.log")
	if err != nil {
		t.Fatalf("CodingAgentAdapter Run failed: %v", err)
	}

	// Verify the underlying agent was called
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
}
