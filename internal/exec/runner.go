package exec

import (
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/logging"
)

// CommandRunner handles execution with logging support
type CommandRunner struct {
	logDir string
	out    io.Writer
}

func NewCommandRunner(logDir string, out io.Writer) *CommandRunner {
	return &CommandRunner{
		logDir: logDir,
		out:    out,
	}
}

func (cr *CommandRunner) Run(args ...string) (string, error) {
	start := time.Now()

	// Check if this is a bd or git command that should be logged
	shouldLog := cr.shouldLogCommand(args)

	var stdout, stderr strings.Builder
	cmd := exec.Command(args[0], args[1:]...)

	if shouldLog {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	} else {
		cmd.Stdout = cr.out
		cmd.Stderr = cr.out
	}

	err := cmd.Run()
	elapsed := time.Since(start)

	// Log the command if needed
	if shouldLog {
		logger := logging.NewCommandLogger(cr.logDir)
		if logErr := logger.LogCommand(args, stdout.String(), stderr.String(), err, start); logErr != nil {
			// If logging fails, we still return the command result
			// but we could also choose to return the logging error
		}

		// Don't output command details to stdout for logged commands
		return stdout.String(), err
	}

	// For non-logged commands (like opencode), use original behavior
	printCommand(cr.out, args)
	printOutcome(cr.out, err, elapsed, strings.Join(args, " "))
	return stdout.String(), err
}

func (cr *CommandRunner) shouldLogCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}

	command := args[0]
	return command == "bd" || command == "git"
}

// These functions are copied from cmd/yolo-runner/exec.go to avoid import cycles
func printCommand(out io.Writer, args []string) {
	if out == nil {
		return
	}
	if isTerminal(out) {
		redacted := redactedCommand(args)
		commandStr := strings.Join(redacted, " ")
		out.Write([]byte("\r\x1b[2K$ " + commandStr + "\r\n"))
		return
	}
	out.Write([]byte("$ " + strings.Join(redactedCommand(args), " ") + "\n"))
}

func redactedCommand(args []string) []string {
	if len(args) >= 3 && args[0] == "opencode" && args[1] == "run" {
		redacted := append([]string{}, args...)
		redacted[2] = "<prompt redacted>"
		return redacted
	}
	return args
}

func printOutcome(out io.Writer, err error, elapsed time.Duration, command string) {
	if out == nil {
		return
	}
	status := "ok"
	exitCode := 0
	if err != nil {
		status = "failed"
		exitCode = exitCodeFromError(err)
	}
	if isTerminal(out) {
		out.Write([]byte("\r\x1b[2K" + status + " (exit=" + string(rune('0'+exitCode)) + ", elapsed=" + formatElapsed(elapsed) + ")\r\n"))
		return
	}
	out.Write([]byte(status + " (exit=" + string(rune('0'+exitCode)) + ", elapsed=" + formatElapsed(elapsed) + ")\n"))
}

func formatElapsed(elapsed time.Duration) string {
	if elapsed < time.Millisecond {
		return "0ms"
	}
	return elapsed.Round(time.Millisecond).String()
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if exitErr != nil && exitErr.ExitCode() != 0 {
		return exitErr.ExitCode()
	}
	return 1
}

func isTerminal(out io.Writer) bool {
	// For testing purposes, we'll assume it's not a terminal
	// In real implementation, this would check if out is a terminal
	return false
}
