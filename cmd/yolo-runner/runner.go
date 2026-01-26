package main

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

	// For non-logged commands (like opencode), use the original behavior
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
