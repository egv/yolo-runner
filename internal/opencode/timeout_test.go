package opencode

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// MockProcess for testing
type MockProcess struct {
	waitCh     chan error
	killCh     chan error
	killCalled bool
	stdin      io.WriteCloser
	stdout     io.ReadCloser
}

func (m *MockProcess) Wait() error {
	return <-m.waitCh
}

func (m *MockProcess) Kill() error {
	m.killCalled = true
	// Close waitCh to unblock any Wait() calls
	close(m.waitCh)
	if m.killCh != nil {
		return <-m.killCh
	}
	return nil
}

func (m *MockProcess) Stdin() io.WriteCloser {
	return m.stdin
}

func (m *MockProcess) Stdout() io.ReadCloser {
	return m.stdout
}

// MockRunner for testing
type MockRunner struct {
	process *MockProcess
	err     error
}

func (m *MockRunner) Start(args []string, env map[string]string, stdoutPath string) (Process, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.process, nil
}

// nopWriteCloser implements io.WriteCloser that does nothing
type nopWriteCloser struct{}

func (n *nopWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (n *nopWriteCloser) Close() error {
	return nil
}

// nopReadCloser implements io.ReadCloser that never returns data
type nopReadCloser struct{}

func (n *nopReadCloser) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (n *nopReadCloser) Close() error {
	return nil
}

// TestOpenCodeTimeoutDetection tests that OpenCode processes that get stuck during initialization are detected
func TestOpenCodeTimeoutDetection(t *testing.T) {
	// Create a mock process that never completes (stuck)
	mockProcess := &MockProcess{
		waitCh: make(chan error), // Never receives anything, simulating stuck process
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	mockProcess.killCh <- nil // Kill succeeds

	runner := &MockRunner{process: mockProcess}

	// Create context with very short timeout for testing
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Use a log path for testing
	logPath := t.TempDir() + "/test.log"
	_ = os.Remove(strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log")

	// This should timeout and return an error, not hang forever
	err := RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{returnError: context.DeadlineExceeded})

	// Should fail due to timeout
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Verify the process was killed
	if !mockProcess.killCalled {
		t.Fatal("expected process.Kill() to be called")
	}

	// The error should be related to context timeout or stall detection
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "stall") {
		t.Fatalf("expected timeout or stall error, got: %v", err)
	}
}

// TestSerenaInitializationFailure tests that Serena language server initialization failures are propagated immediately
func TestSerenaInitializationFailure(t *testing.T) {
	// Create a mock process that writes Serena initialization error to stderr
	logPath := t.TempDir() + "/test.log"
	errorLog := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	stderrFile, err := os.Create(errorLog)
	if err != nil {
		t.Fatalf("failed to create stderr log: %v", err)
	}

	// Write the Serena initialization error
	stderrContent := `The language server manager is not initialized, indicating a problem during project activation. Inform the user, telling them to inspect Serena's logs in order to determine the issue. IMPORTANT: Wait for further instructions before you continue!`
	if _, err := stderrFile.WriteString(stderrContent); err != nil {
		t.Fatalf("failed to write to stderr: %v", err)
	}
	stderrFile.Close()

	mockProcess := &MockProcess{
		waitCh: make(chan error, 1),
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	// Process exits with non-zero code to indicate failure
	mockProcess.waitCh <- errors.New("exit status 1")
	mockProcess.killCh <- nil

	runner := &MockRunner{process: mockProcess}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// This should fail immediately due to Serena initialization failure
	err = RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{returnError: context.DeadlineExceeded})

	if err == nil {
		t.Fatal("expected error due to Serena initialization failure, got nil")
	}

	// Should detect the specific Serena initialization error
	if !strings.Contains(err.Error(), "language server manager is not initialized") {
		t.Fatalf("expected Serena initialization error in message, got: %v", err)
	}
}

// TestSerenaInitializationFailureReturnsImmediately tests that Serena initialization failures
// are detected without waiting for process completion.
func TestSerenaInitializationFailureReturnsImmediately(t *testing.T) {
	logPath := t.TempDir() + "/test.log"
	errorLog := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	stderrFile, err := os.Create(errorLog)
	if err != nil {
		t.Fatalf("failed to create stderr log: %v", err)
	}
	stderrContent := "language server manager is not initialized"
	if _, err := stderrFile.WriteString(stderrContent); err != nil {
		t.Fatalf("failed to write to stderr: %v", err)
	}
	stderrFile.Close()

	mockProcess := &MockProcess{
		waitCh: make(chan error),
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	mockProcess.killCh <- nil

	runner := &MockRunner{process: mockProcess}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{returnError: context.DeadlineExceeded})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error due to Serena initialization failure, got nil")
		}
		if !strings.Contains(err.Error(), "language server manager is not initialized") {
			t.Fatalf("expected Serena initialization error in message, got: %v", err)
		}
		if !mockProcess.killCalled {
			t.Fatal("expected process to be killed on Serena initialization failure")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected RunWithACP to return immediately on Serena initialization failure")
	}
}

// TestOpenCodeHealthCheck tests that health checks for OpenCode session progress work
func TestOpenCodeHealthCheck(t *testing.T) {
	// Create a mock process that appears to be running but makes no progress
	mockProcess := &MockProcess{
		waitCh: make(chan error), // Never completes
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	mockProcess.killCh <- nil

	runner := &MockRunner{process: mockProcess}

	// Use very short timeout for testing
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	logPath := t.TempDir() + "/test.log"

	// Should detect lack of progress and fail
	err := RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{returnError: context.DeadlineExceeded})

	if err == nil {
		t.Fatal("expected error due to no progress, got nil")
	}

	// Should be detected as a stall
	if !strings.Contains(err.Error(), "stall") && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected stall or timeout error, got: %v", err)
	}
}

// TestKillStuckSubprocesses tests that stuck subprocesses are killed after timeout threshold
func TestKillStuckSubprocesses(t *testing.T) {
	// Create a mock process that gets stuck
	mockProcess := &MockProcess{
		waitCh: make(chan error), // Never completes
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	mockProcess.killCh <- nil // Kill succeeds

	runner := &MockRunner{process: mockProcess}

	// Use very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()

	logPath := t.TempDir() + "/test.log"

	// Should attempt to kill the stuck process
	err := RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{returnError: context.DeadlineExceeded})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// Verify kill was called
	if !mockProcess.killCalled {
		t.Fatal("expected process.Kill() to be called on stuck process")
	}
}

// TestTimeoutBehaviorVerification verifies that timeout behavior works correctly
func TestTimeoutBehaviorVerification(t *testing.T) {
	testCases := []struct {
		name            string
		timeoutMs       int
		processBehavior func(*MockProcess)
		acpClient       ACPClient
		expectedError   string
	}{
		{
			name:      "very_fast_timeout",
			timeoutMs: 25,
			processBehavior: func(mp *MockProcess) {
				// Process never responds
			},
			acpClient:     &mockACPClient{}, // Blocks until context is canceled
			expectedError: "stall",
		},
		{
			name:      "completes_before_timeout",
			timeoutMs: 1000,
			processBehavior: func(mp *MockProcess) {
				// Process completes successfully
				mp.waitCh <- nil
			},
			acpClient:     ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error { return nil }),
			expectedError: "", // Should not error
		},
		{
			name:      "fails_immediately",
			timeoutMs: 1000,
			processBehavior: func(mp *MockProcess) {
				// Process fails immediately with Serena error
				mp.waitCh <- errors.New("language server manager is not initialized")
			},
			acpClient:     ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error { return nil }),
			expectedError: "language server manager is not initialized",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockProcess := &MockProcess{
				waitCh: make(chan error, 1),
				killCh: make(chan error, 1),
				stdin:  &nopWriteCloser{},
				stdout: &nopReadCloser{},
			}
			mockProcess.killCh <- nil

			// Apply the test-specific behavior
			tc.processBehavior(mockProcess)

			runner := &MockRunner{process: mockProcess}

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(tc.timeoutMs)*time.Millisecond)
			defer cancel()

			logPath := t.TempDir() + "/test.log"

			err := RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, tc.acpClient)

			if tc.expectedError == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.expectedError)
				}
				if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("expected error containing %q, got: %v", tc.expectedError, err)
				}
			}
		})
	}
}

// TestClearErrorMessageLogging tests that clear error messages are logged when language server fails
func TestClearErrorMessageLogging(t *testing.T) {
	// Capture stderr to verify error logging
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Create a mock process that fails with Serena error
	mockProcess := &MockProcess{
		waitCh: make(chan error, 1),
		killCh: make(chan error, 1),
		stdin:  &nopWriteCloser{},
		stdout: &nopReadCloser{},
	}
	// Process exits with non-zero code to indicate failure
	mockProcess.waitCh <- errors.New("exit status 1")
	mockProcess.killCh <- nil

	runner := &MockRunner{process: mockProcess}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logPath := t.TempDir() + "/test.log"
	errorLog := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	stderrFile, err := os.Create(errorLog)
	if err != nil {
		t.Fatalf("failed to create stderr log: %v", err)
	}
	stderrContent := "language server manager is not initialized"
	if _, err := stderrFile.WriteString(stderrContent); err != nil {
		t.Fatalf("failed to write to stderr: %v", err)
	}
	stderrFile.Close()

	err = RunWithACP(ctx, "test-issue", "/tmp", "test prompt", "", "", "", logPath, runner, &mockACPClient{returnError: context.DeadlineExceeded})

	// Restore stderr and capture output
	w.Close()
	os.Stderr = oldStderr

	var stderrOutput strings.Builder
	io.Copy(&stderrOutput, r)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify clear error message was logged
	stderrStr := stderrOutput.String()
	if !strings.Contains(stderrStr, "language server manager is not initialized") {
		t.Fatalf("expected clear error message in stderr, got: %s", stderrStr)
	}

	// Error should be propagated
	if !strings.Contains(err.Error(), "language server manager is not initialized") {
		t.Fatalf("expected Serena initialization error in return value, got: %v", err)
	}
}

// mockACPClient is a simple mock ACP client that can be configured to return immediately
// or simulate specific behaviors without attempting real ACP protocol initialization
type mockACPClient struct {
	returnImmediately bool
	returnError       error
}

func (m *mockACPClient) Run(ctx context.Context, issueID string, logPath string) error {
	if m.returnImmediately {
		return m.returnError
	}
	// Simulate blocking until context is cancelled
	<-ctx.Done()
	return ctx.Err()
}
