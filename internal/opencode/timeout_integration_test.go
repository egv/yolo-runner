package opencode

import (
	"context"
	"testing"
	"time"
)

func TestTimeoutMechanismIntegration(t *testing.T) {
	// Create a temporary log file
	logPath := t.TempDir() + "/test.log"

	// Create mock process that never completes
	mockProcess := &MockProcess{
		waitCh: make(chan error), // Never completes
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	mockProcess.killCh <- nil // Kill succeeds

	runner := &MockRunner{process: mockProcess}

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// This should timeout due to watchdog detecting stuck process
	err := RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Should be related to stall or timeout
	if !containsTimeoutRelated(err.Error()) {
		t.Fatalf("expected timeout-related error, got: %v", err)
	}

	// Verify process was killed
	if !mockProcess.killCalled {
		t.Fatal("expected process.Kill() to be called")
	}
}

func containsTimeoutRelated(err string) bool {
	return containsAny(err, []string{"stall", "timeout", "deadline exceeded"})
}

func containsAny(s string, substrings []string) bool {
	for _, substr := range substrings {
		if len(substr) > 0 && len(s) >= len(substr) {
			// Simple substring check
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
