package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/beads"
	"github.com/anomalyco/yolo-runner/internal/exec"
	"github.com/anomalyco/yolo-runner/internal/runner"
	"github.com/anomalyco/yolo-runner/internal/ui/tui"
	"github.com/anomalyco/yolo-runner/internal/vcs/git"
)

func TestIntegration_AllRequirementsWorkTogether(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "integration-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logDir := filepath.Join(tempDir, "runner-logs")

	// Create command runner that logs to temp directory
	commandRunner := exec.NewCommandRunner(logDir, nil)

	// Test that bd commands route to log files
	beadsAdapter := beads.New(commandRunner)
	_, err = beadsAdapter.Ready("test-root")
	if err == nil {
		t.Log("WARNING: bd ready command should have failed without proper setup")
	}

	// Verify log file was created
	logFiles, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log directory: %v", err)
	}

	if len(logFiles) == 0 {
		t.Fatalf("expected log files to be created for bd commands")
	}

	// Test that git commands route to log files
	gitAdapter := git.NewGitCommandAdapter(commandRunner)
	gitAdapterInstance := git.New(gitAdapter)
	err = gitAdapterInstance.AddAll()
	if err == nil {
		t.Log("WARNING: git add command should have failed without proper setup")
	}

	// Verify additional log files were created
	logFilesAfter, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log directory after git command: %v", err)
	}

	if len(logFilesAfter) <= len(logFiles) {
		t.Fatalf("expected additional log files for git commands")
	}

	// Test that TUI shows high-level action labels
	model := tui.NewModelWithStop(func() time.Time { return time.Now() }, nil)

	// Set up a basic task context so the view shows properly
	var cmd tea.Cmd
	var updatedModel tea.Model = model
	updatedModel, cmd = updatedModel.Update(runner.Event{
		Type:    "select_task",
		IssueID: "task-1",
		Title:   "Example Task",
	})
	model = updatedModel.(tui.Model)
	_ = cmd // Suppress unused variable warning

	// Simulate different events and verify they show user-friendly labels
	testEvents := []struct {
		eventType     string
		expectedLabel string
	}{
		{"select_task", "getting task info"},
		{"beads_update", "updating task status"},
		{"git_add", "adding changes"},
		{"git_commit", "committing changes"},
	}

	for _, test := range testEvents {
		// Update model with current event
		var updatedModel tea.Model = model
		updatedModel, _ = updatedModel.Update(runner.Event{
			Type:    runner.EventType(test.eventType),
			IssueID: "task-1",
			Title:   "Example Task",
		})
		model = updatedModel.(tui.Model)

		view := model.View()

		if !containsView(view, test.expectedLabel) {
			t.Errorf("expected view to contain '%s', got: %s", test.expectedLabel, view)
		}
	}
}

func containsView(view, expected string) bool {
	// Simple substring search for the expected label
	return len(view) > 0 && findSubstring(view, expected)
}

func findSubstring(s, substr string) bool {
	// Simple substring search implementation
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
