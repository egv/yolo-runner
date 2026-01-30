package opencode

import (
	"time"
)
import (
	"testing"
)

func TestMockProcess(t *testing.T) {
	// Test that MockProcess works correctly
	mockProcess := &MockProcess{
		waitCh: make(chan error, 1),
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}

	// Send value to waitCh so Wait doesn't block
	mockProcess.waitCh <- nil

	// Send value to killCh so Kill doesn't block
	mockProcess.killCh <- nil

	err := mockProcess.Wait()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Test Kill method
	err = mockProcess.Kill()
	if err != nil {
		t.Fatalf("expected no error from Kill, got: %v", err)
	}

	if !mockProcess.killCalled {
		t.Fatal("expected Kill to be called")
	}
}

// TestMockProcessKillUnblocksWait verifies that calling Kill() on MockProcess unblocks Wait()
func TestMockProcessKillUnblocksWait(t *testing.T) {
	mockProcess := &MockProcess{
		waitCh: make(chan error),
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	mockProcess.killCh <- nil

	// Start waiting in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- mockProcess.Wait()
	}()

	// Wait a bit to ensure the goroutine is blocking on waitCh
	time.Sleep(10 * time.Millisecond)

	// Kill the process
	err := mockProcess.Kill()
	if err != nil {
		t.Fatalf("Kill() returned error: %v", err)
	}

	// Wait for Wait() to return with a timeout
	select {
	case err := <-done:
		// Wait() returned successfully
		if err != nil {
			t.Fatalf("Wait() returned error after Kill(): %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Wait() did not return after Kill() was called (timed out)")
	}

	if !mockProcess.killCalled {
		t.Fatal("killCalled was not set to true")
	}
}

