package beads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/exec"
)

func TestBeadsCommandsRouteOutputToLogFiles(t *testing.T) {
	// Create a temporary directory for logs and repo
	tempDir, err := os.MkdirTemp("", "beads-logs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logDir := filepath.Join(tempDir, "runner-logs")

	// Create a CommandRunner that logs to our temp directory
	commandRunner := exec.NewCommandRunner(logDir, nil) // nil = no stdout output
	adapter := New(commandRunner)

	// Test various bd commands that should route output to logs
	testCases := []struct {
		name        string
		testFunc    func() error
		expectError bool
	}{
		{
			name: "Ready command",
			testFunc: func() error {
				_, err := adapter.Ready("root")
				return err
			},
			expectError: true, // bd command will fail without proper setup
		},
		{
			name: "Show command",
			testFunc: func() error {
				_, err := adapter.Show("task-1")
				return err
			},
			expectError: true,
		},
		{
			name: "UpdateStatus command",
			testFunc: func() error {
				return adapter.UpdateStatus("task-1", "in_progress")
			},
			expectError: true,
		},
		{
			name: "Close command",
			testFunc: func() error {
				return adapter.Close("task-1")
			},
			expectError: true,
		},
		{
			name: "Sync command",
			testFunc: func() error {
				return adapter.Sync()
			},
			expectError: false, // sync may not error, just no output
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Count log files before execution
			logFilesBefore, err := os.ReadDir(logDir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("failed to read log dir before: %v", err)
			}

			// Execute the command
			err = tc.testFunc()

			// We expect an error since bd commands won't actually work
			// without a proper beads setup, but we're testing logging behavior
			if tc.expectError && err == nil {
				t.Fatalf("expected error but got none")
			}

			// Count log files after execution
			logFilesAfter, err := os.ReadDir(logDir)
			if err != nil {
				t.Fatalf("failed to read log dir after: %v", err)
			}

			// Verify that a new log file was created
			if len(logFilesAfter) <= len(logFilesBefore) {
				t.Fatalf("expected new log file to be created, had %d before, %d after",
					len(logFilesBefore), len(logFilesAfter))
			}

			// Find the newest log file
			var newestLogFile string
			var newestModTime int64 = 0
			for _, logFile := range logFilesAfter {
				info, err := logFile.Info()
				if err != nil {
					continue
				}
				if info.ModTime().UnixNano() > newestModTime {
					newestModTime = info.ModTime().UnixNano()
					newestLogFile = logFile.Name()
				}
			}

			if newestLogFile == "" {
				t.Fatalf("no log file found")
			}

			// Read log file content
			logFilePath := filepath.Join(logDir, newestLogFile)
			logContent, err := os.ReadFile(logFilePath)
			if err != nil {
				t.Fatalf("failed to read log file: %v", err)
			}

			// Verify that log file contains expected sections
			logContentStr := string(logContent)
			expectedSections := []string{"Command:", "Start Time:", "Elapsed:", "=== STDOUT ===", "=== STDERR ==="}
			for _, section := range expectedSections {
				if !strings.Contains(logContentStr, section) {
					t.Fatalf("log file should contain section '%s', got: %s", section, logContentStr)
				}
			}

			// Verify that log file contains a bd command
			if !strings.Contains(logContentStr, "bd ") {
				t.Fatalf("log file should contain bd command, got: %s", logContentStr)
			}
		})
	}
}
