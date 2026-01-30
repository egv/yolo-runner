package opencode

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const serenaInitFailureMarker = "language server manager is not initialized"

// monitorInitFailures monitors stderr logs for Serena initialization failures
// and reports the error if detected.
func monitorInitFailures(ctx context.Context, stderrPath string, errCh chan<- error, since time.Time) {
	if stderrPath == "" {
		return
	}
	if errCh == nil {
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if line, ok := findSerenaInitErrorSince(stderrPath, since); ok {
				serenaErr := fmt.Errorf("serena initialization failed: %s", line)
				writeConsoleLine(os.Stderr, serenaErr.Error())
				select {
				case errCh <- serenaErr:
				default:
				}
				return
			}
		}
	}
}

// findSerenaInitError checks if stderr log contains Serena initialization error
// and returns the matching line if present.
func findSerenaInitErrorSince(stderrPath string, since time.Time) (string, bool) {
	info, err := os.Stat(stderrPath)
	if err != nil {
		return "", false
	}
	if !since.IsZero() && info.ModTime().Before(since) {
		return "", false
	}

	file, err := os.Open(stderrPath)
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, serenaInitFailureMarker) {
			return line, true
		}
	}
	return "", false
}
