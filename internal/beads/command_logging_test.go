package beads

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/exec"
	"github.com/egv/yolo-runner/v2/internal/logging"
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
		name     string
		testFunc func() error
	}{
		{
			name: "Ready command",
			testFunc: func() error {
				_, err := adapter.Ready("root")
				return err
			},
		},
		{
			name: "Show command",
			testFunc: func() error {
				_, err := adapter.Show("task-1")
				return err
			},
		},
		{
			name: "UpdateStatus command",
			testFunc: func() error {
				return adapter.UpdateStatus("task-1", "in_progress")
			},
		},
		{
			name: "Close command",
			testFunc: func() error {
				return adapter.Close("task-1")
			},
		},
		{
			name: "Sync command",
			testFunc: func() error {
				return adapter.Sync()
			},
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

			// Commands may succeed or fail depending on environment; we only
			// verify that command logging works.

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
			if !strings.Contains(command, "bd ") {
				t.Fatalf("log file should contain bd command, got: %s", logContentStr)
			}
		})
	}
}
