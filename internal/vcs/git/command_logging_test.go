package git

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/internal/exec"
	"github.com/egv/yolo-runner/internal/logging"
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
			expectError: false,
		},
		{
			name: "IsDirty command",
			testFunc: func() error {
				_, err := adapter.IsDirty()
				return err
			},
			expectError: false,
		},
		{
			name: "RevParseHead command",
			testFunc: func() error {
				_, err := adapter.RevParseHead()
				return err
			},
			expectError: false,
		},
		{
			name: "StatusPorcelain command",
			testFunc: func() error {
				_, err := adapter.StatusPorcelain()
				return err
			},
			expectError: false,
		},
		{
			name: "RestoreAll command",
			testFunc: func() error {
				return adapter.RestoreAll()
			},
			expectError: false,
		},
		{
			name: "CleanAll command",
			testFunc: func() error {
				return adapter.CleanAll()
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Execute the command
			err = tc.testFunc()

			// We may not expect errors since git commands might not error without a repo
			// but we're testing the logging behavior
			if tc.expectError && err == nil {
				t.Fatalf("expected error but got none")
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

			// Find all log files and check if any contain the expected command
			foundCommandInLogs := false
			for _, logFile := range logFilesAfter {
				logFilePath := filepath.Join(logDir, logFile.Name())
				logContent, err := os.ReadFile(logFilePath)
				if err != nil {
					continue
				}
				logContentStr := string(logContent)
				if strings.Contains(logContentStr, "git ") {
					foundCommandInLogs = true
					break
				}
			}

			if !foundCommandInLogs {
				t.Fatalf("expected to find git command in log files, but didn't find any")
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

			// Verify that log file is valid JSON and includes required fields
			logContentStr := string(logContent)
			lines := strings.Split(strings.TrimSpace(logContentStr), "\n")
			if len(lines) == 0 {
				t.Fatal("expected non-empty log file")
			}
			line := lines[len(lines)-1]
			if err := logging.ValidateStructuredLogLine([]byte(line)); err != nil {
				t.Fatalf("invalid structured log line: %v", err)
			}

			entry := map[string]interface{}{}
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("invalid json log entry: %v", err)
			}
			command, _ := entry["command"].(string)
			if !strings.Contains(command, "git ") {
				t.Fatalf("log file should contain git command, got: %s", logContentStr)
			}
		})
	}
}
