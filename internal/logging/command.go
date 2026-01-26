package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CommandLogger handles logging of command stdout/stderr to files
type CommandLogger struct {
	logDir string
}

// NewCommandLogger creates a new command logger
func NewCommandLogger(logDir string) *CommandLogger {
	return &CommandLogger{
		logDir: logDir,
	}
}

// LogCommand logs a command's execution details, stdout, and stderr to files
func (cl *CommandLogger) LogCommand(command []string, stdout string, stderr string, err error, startTime time.Time) error {
	if cl.logDir == "" {
		return nil
	}

	// Ensure log directory exists
	if err := os.MkdirAll(cl.logDir, 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create a timestamped log file for this command
	timestamp := startTime.UTC().Format("20060102_150405_000000")
	commandName := strings.Join(command[:min(3, len(command))], "_")
	safeCommandName := strings.ReplaceAll(commandName, "/", "_")
	safeCommandName = strings.ReplaceAll(safeCommandName, " ", "_")

	logFileName := fmt.Sprintf("%s_%s.log", timestamp, safeCommandName)
	logFilePath := filepath.Join(cl.logDir, logFileName)

	// Open log file for writing
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	// Write command details
	elapsed := time.Since(startTime)
	fmt.Fprintf(logFile, "Command: %s\n", strings.Join(command, " "))
	fmt.Fprintf(logFile, "Start Time: %s\n", startTime.UTC().Format(time.RFC3339))
	fmt.Fprintf(logFile, "Elapsed: %s\n", elapsed.Round(time.Millisecond))

	if err != nil {
		fmt.Fprintf(logFile, "Error: %v\n", err)
	}

	// Write stdout if present
	if stdout != "" {
		fmt.Fprintf(logFile, "\n=== STDOUT ===\n%s\n", stdout)
	} else {
		fmt.Fprintf(logFile, "\n=== STDOUT ===\n(no output)\n")
	}

	// Write stderr if present
	if stderr != "" {
		fmt.Fprintf(logFile, "\n=== STDERR ===\n%s\n", stderr)
	} else {
		fmt.Fprintf(logFile, "\n=== STDERR ===\n(no output)\n")
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
