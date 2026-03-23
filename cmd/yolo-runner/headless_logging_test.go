//go:build legacy

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/exec"
)

// TestHeadlessModeShowsHighLevelLabels verifies that in headless mode,
// the runner shows high-level action labels but no command echo
func TestHeadlessModeShowsHighLevelLabels(t *testing.T) {
	tempDir := t.TempDir()
	logDir := filepath.Join(tempDir, "runner-logs")

	// Create a command runner that logs to our temp directory
	// nil for stdout means no output should go to stdout
	commandRunner := exec.NewCommandRunner(logDir, nil)

	// Execute a git command via the runner
	output, err := commandRunner.Run("git", "status", "--porcelain")
	// We expect an error because we're not in a git repo, but we're testing behavior
	_ = err
	_ = output

	// Verify that a log file was created
	logFiles, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}
	if len(logFiles) == 0 {
		t.Fatalf("expected log file to be created")
	}

	// Verify that stdout is empty (we passed nil as output)
	// This verifies that logged commands don't echo to stdout
	// We can't directly test this here since we passed nil,
	// but we'll verify the behavior in a separate test

	// Read the log file to verify command was logged
	var newestLogFile string
	var newestModTime int64 = 0
	for _, logFile := range logFiles {
		info, err := logFile.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > newestModTime {
			newestModTime = info.ModTime().UnixNano()
			newestLogFile = logFile.Name()
		}
	}

	logContent, err := os.ReadFile(filepath.Join(logDir, newestLogFile))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	logStr := string(logContent)
	// Verify log contains a valid structured entry with command and streams
	line := strings.TrimSpace(logStr)
	entry := map[string]interface{}{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("log should contain valid JSON: %v", err)
	}
	if _, ok := entry["command"]; !ok {
		t.Fatalf("log should contain command field: %s", logStr)
	}
	if _, ok := entry["stdout"]; !ok {
		t.Fatalf("log should contain stdout field: %s", logStr)
	}
	if _, ok := entry["stderr"]; !ok {
		t.Fatalf("log should contain stderr field: %s", logStr)
	}
}

// TestCommandRunnerDoesNotEchoToStdoutForLoggedCommands verifies that
// when CommandRunner is used with a writer, it doesn't echo logged commands
func TestCommandRunnerDoesNotEchoToStdoutForLoggedCommands(t *testing.T) {
	tempDir := t.TempDir()
	logDir := filepath.Join(tempDir, "runner-logs")

	// Create a buffer to capture stdout
	stdoutBuffer := &bytes.Buffer{}

	// Create a command runner with stdout buffer
	commandRunner := exec.NewCommandRunner(logDir, stdoutBuffer)

	// Execute a git command (which is a logged command)
	_, err := commandRunner.Run("git", "status", "--porcelain")
	// We expect an error because we're not in a git repo
	_ = err

	// Verify that NO command echo appears in stdout
	stdoutStr := stdoutBuffer.String()
	if strings.Contains(stdoutStr, "$ git") {
		t.Fatalf("stdout should not contain command echo, got: %s", stdoutStr)
	}
	if strings.Contains(stdoutStr, "git status") {
		t.Fatalf("stdout should not contain command echo, got: %s", stdoutStr)
	}
	if strings.Contains(stdoutStr, "ok (exit=") {
		t.Fatalf("stdout should not contain command outcome for logged commands, got: %s", stdoutStr)
	}
	if strings.Contains(stdoutStr, "failed (exit=") {
		t.Fatalf("stdout should not contain command outcome for logged commands, got: %s", stdoutStr)
	}

	// Verify that a log file was created with the command output
	logFiles, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}
	if len(logFiles) == 0 {
		t.Fatalf("expected log file to be created for git command")
	}
}

// TestCommandRunnerDoesNotLogNonBdGitCommands verifies that
// non-bd/non-git commands are not logged but do echo to stdout
func TestCommandRunnerDoesNotLogNonBdGitCommands(t *testing.T) {
	tempDir := t.TempDir()
	logDir := filepath.Join(tempDir, "runner-logs")

	// Create a buffer to capture stdout
	stdoutBuffer := &bytes.Buffer{}

	// Create a command runner with stdout buffer
	commandRunner := exec.NewCommandRunner(logDir, stdoutBuffer)

	// Execute a non-bd/non-git command (echo is safe to use)
	// Using /bin/echo instead of shell to avoid any confusion
	_, err := commandRunner.Run("/bin/echo", "hello world")
	if err != nil {
		t.Fatalf("unexpected error running echo command: %v", err)
	}

	// Verify that output is in stdout
	stdoutStr := stdoutBuffer.String()
	if !strings.Contains(stdoutStr, "$ /bin/echo hello world") {
		t.Fatalf("stdout should contain command echo for non-logged commands, got: %s", stdoutStr)
	}
	if !strings.Contains(stdoutStr, "ok (exit=0") {
		t.Fatalf("stdout should contain command outcome for non-logged commands, got: %s", stdoutStr)
	}

	// Verify that NO log file was created
	logFiles, err := os.ReadDir(logDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read log dir: %v", err)
	}
	// Non-logged commands should not create log files
	// The log directory might not exist yet
	if len(logFiles) > 0 {
		// If log files exist, make sure none are for this non-bd/non-git command
		for _, logFile := range logFiles {
			if strings.Contains(logFile.Name(), "echo") {
				t.Fatalf("non-bd/non-git commands should not be logged, found: %s", logFile.Name())
			}
		}
	}
}

// TestBDCommandsAreRoutedToLogFiles verifies that bd commands
// are logged to files and not echoed to stdout
func TestBDCommandsAreRoutedToLogFiles(t *testing.T) {
	tempDir := t.TempDir()
	logDir := filepath.Join(tempDir, "runner-logs")

	// Create a buffer to capture stdout
	stdoutBuffer := &bytes.Buffer{}

	// Create a command runner with stdout buffer
	commandRunner := exec.NewCommandRunner(logDir, stdoutBuffer)

	// Execute a bd command (which is a logged command)
	_, err := commandRunner.Run("bd", "ready", "--parent", "test-root")
	// We expect an error because bd won't work without proper setup
	_ = err

	// Verify that NO command echo appears in stdout
	stdoutStr := stdoutBuffer.String()
	if strings.Contains(stdoutStr, "$ bd") {
		t.Fatalf("stdout should not contain bd command echo, got: %s", stdoutStr)
	}

	// Verify that a log file was created
	logFiles, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}
	if len(logFiles) == 0 {
		t.Fatalf("expected log file to be created for bd command")
	}

	// Verify the log file contains the bd command
	var newestLogFile string
	var newestModTime int64 = 0
	for _, logFile := range logFiles {
		info, err := logFile.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() > newestModTime {
			newestModTime = info.ModTime().UnixNano()
			newestLogFile = logFile.Name()
		}
	}

	logContent, err := os.ReadFile(filepath.Join(logDir, newestLogFile))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	logStr := strings.TrimSpace(string(logContent))
	lines := strings.Split(logStr, "\n")
	if len(lines) == 0 {
		t.Fatalf("expected non-empty log content")
	}
	line := lines[len(lines)-1]
	entry := map[string]interface{}{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("log should contain valid JSON: %v", err)
	}
	command, _ := entry["command"].(string)
	if !strings.Contains(command, "bd ") {
		t.Fatalf("log should contain bd command, got: %s", logStr)
	}
	if _, ok := entry["stdout"]; !ok {
		t.Fatalf("log should contain stdout field: %s", logStr)
	}
	if _, ok := entry["stderr"]; !ok {
		t.Fatalf("log should contain stderr field: %s", logStr)
	}
}
