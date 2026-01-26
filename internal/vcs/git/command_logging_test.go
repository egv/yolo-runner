package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/exec"
)

func TestGitCommandsRouteOutputToLogFiles(t *testing.T) {
	// Create a temporary directory for logs and repo
	tempDir, err := os.MkdirTemp("", "git-logs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logDir := filepath.Join(tempDir, "runner-logs")

	// Create a CommandRunner that logs to our temp directory
	commandRunner := exec.NewCommandRunner(logDir, nil) // nil = no stdout output
	gitAdapter := NewGitCommandAdapter(commandRunner)
	adapter := New(gitAdapter)

	// Test various git commands that should route output to logs
	testCases := []struct {
		name        string
		testFunc    func() error
		expectError bool
	}{
		{
			name: "AddAll command",
			testFunc: func() error {
				return adapter.AddAll()
			},
			expectError: false, // git may not error without proper repo
		},
		{
			name: "Commit command",
			testFunc: func() error {
				return adapter.Commit("test commit")
			},
			expectError: true,
		},
		{
			name: "IsDirty command",
			testFunc: func() error {
				_, err := adapter.IsDirty()
				return err
			},
			expectError: true,
		},
		{
			name: "RevParseHead command",
			testFunc: func() error {
				_, err := adapter.RevParseHead()
				return err
			},
			expectError: true,
		},
		{
			name: "StatusPorcelain command",
			testFunc: func() error {
				_, err := adapter.StatusPorcelain()
				return err
			},
			expectError: true,
		},
		{
			name: "RestoreAll command",
			testFunc: func() error {
				return adapter.RestoreAll()
			},
			expectError: true,
		},
		{
			name: "CleanAll command",
			testFunc: func() error {
				return adapter.CleanAll()
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Count log files before execution
			logFilesBefore, err := os.ReadDir(logDir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("failed to read log dir before: %v", err)
			}

			// Execute command
			err = tc.testFunc()

			// We expect an error since git commands won't actually work
			// without a proper git repo, but we're testing logging behavior
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

			// Verify that log file contains a git command
			if !strings.Contains(logContentStr, "git ") {
				t.Fatalf("log file should contain git command, got: %s", logContentStr)
			}
		})
	}
}
