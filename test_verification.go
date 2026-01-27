package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/exec"
)

func main() {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "yolo-test")
	if err != nil {
		fmt.Printf("Failed to create temp dir: %v\n", err)
		return
	}
	defer os.RemoveAll(tempDir)

	logDir := filepath.Join(tempDir, "logs")

	// Test 1: bd commands should route to logs
	fmt.Println("=== Test 1: bd commands route to logs ===")
	commandRunner := exec.NewCommandRunner(logDir, nil)

	// This should fail but create a log file
	_, err = commandRunner.Run("bd", "ready", "--parent", "fake-root")
	if err != nil {
		fmt.Printf("bd command failed as expected: %v\n", err)
	}

	// Check if log file was created
	logFiles, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Printf("Failed to read log dir: %v\n", err)
		return
	}

	if len(logFiles) > 0 {
		fmt.Printf("✓ Log file created: %s\n", logFiles[0].Name())

		// Check log file content
		logPath := filepath.Join(logDir, logFiles[0].Name())
		content, err := os.ReadFile(logPath)
		if err != nil {
			fmt.Printf("Failed to read log file: %v\n", err)
			return
		}

		contentStr := string(content)
		if strings.Contains(contentStr, "Command: bd ready") {
			fmt.Printf("✓ Log file contains bd command\n")
		}
		if strings.Contains(contentStr, "=== STDOUT ===") {
			fmt.Printf("✓ Log file contains stdout section\n")
		}
		if strings.Contains(contentStr, "=== STDERR ===") {
			fmt.Printf("✓ Log file contains stderr section\n")
		}
	} else {
		fmt.Printf("✗ No log file created\n")
		return
	}

	// Test 2: git commands should route to logs
	fmt.Println("\n=== Test 2: git commands route to logs ===")

	// This should fail but create a log file
	_, err = commandRunner.Run("git", "status")
	if err != nil {
		fmt.Printf("git command failed as expected: %v\n", err)
	}

	// Check if another log file was created
	logFiles2, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Printf("Failed to read log dir: %v\n", err)
		return
	}

	if len(logFiles2) > len(logFiles) {
		fmt.Printf("✓ Additional log file created for git command\n")

		// Check the newest log file content
		newestFile := logFiles2[len(logFiles2)-1]
		logPath := filepath.Join(logDir, newestFile.Name())
		content, err := os.ReadFile(logPath)
		if err != nil {
			fmt.Printf("Failed to read log file: %v\n", err)
			return
		}

		contentStr := string(content)
		if strings.Contains(contentStr, "Command: git status") {
			fmt.Printf("✓ Log file contains git command\n")
		}
	} else {
		fmt.Printf("✗ No additional log file created for git command\n")
	}

	// Test 3: non-bd/git commands should not be logged
	fmt.Println("\n=== Test 3: non-bd/git commands should not be logged ===")

	// Count files before
	logFiles3, _ := os.ReadDir(logDir)
	beforeCount := len(logFiles3)

	// Run a non-bd/git command
	_, err = commandRunner.Run("echo", "hello world")
	if err != nil {
		fmt.Printf("echo command failed: %v\n", err)
	}

	// Count files after
	logFiles4, _ := os.ReadDir(logDir)
	afterCount := len(logFiles4)

	if afterCount == beforeCount {
		fmt.Printf("✓ No additional log file created for non-bd/git command\n")
	} else {
		fmt.Printf("✗ Unexpected log file created for non-bd/git command\n")
	}

	fmt.Println("\n=== All tests completed ===")
}
