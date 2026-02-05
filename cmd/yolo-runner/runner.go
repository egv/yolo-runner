package main

import (
	"fmt"
	"io"
	"os"
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

	var stdoutBuf, stderrBuf strings.Builder
	cmd := exec.Command(args[0], args[1:]...)

	// Disable pager for commands that might use one (e.g., tk show)
	cmd.Env = append(cmd.Environ(), "PAGER=cat")

	// For tk commands, capture output silently (don't print to terminal)
	// For other commands, both capture and print
	if len(args) > 0 && args[0] == "tk" {
		fmt.Fprintf(os.Stderr, "DEBUG: Suppressing tk output for %v\n", args)
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
	} else {
		cmd.Stdout = io.MultiWriter(&stdoutBuf, cr.out)
		cmd.Stderr = io.MultiWriter(&stderrBuf, cr.out)
	}

	err := cmd.Run()
	elapsed := time.Since(start)

	// Log the command if needed
	if shouldLog {
		logger := logging.NewCommandLogger(cr.logDir)
		if logErr := logger.LogCommand(args, stdoutBuf.String(), stderrBuf.String(), err, start); logErr != nil {
			// If logging fails, we still return the command result
			// but we could also choose to return the logging error
		}
	}

	// Print command outcome (only for non-tk commands, or if there's an error)
	if len(args) == 0 || args[0] != "tk" || err != nil {
		printCommand(cr.out, args)
		printOutcome(cr.out, err, elapsed, strings.Join(args, " "))
	}
	return stdoutBuf.String(), err
}

func (cr *CommandRunner) shouldLogCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}

	command := args[0]
	return command == "bd" || command == "git"
}
