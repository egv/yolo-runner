//go:build windows

package codingagents

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"golang.org/x/sys/windows"
)

func TestGenericCLIRunnerAdapterManagedSupervisorCleansUpSpawnedChildrenOnCompletionWindows(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	childPIDPath := filepath.Join(tempDir, "child.pid")

	adapter := NewGenericCLIRunnerAdapter("custom-cli", os.Args[0], []string{
		"-test.run=^TestManagedSupervisorWindowsHelper$",
		"--",
		"complete-parent",
		childPIDPath,
	}, nil)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-process-job-complete",
		RepoRoot: tempDir,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %q (%s)", result.Status, result.Reason)
	}

	assertProcessEventuallyStopped(t, waitForWindowsPIDFile(t, childPIDPath))
}

func TestGenericCLIRunnerAdapterManagedSupervisorCleansUpSpawnedChildrenOnTimeoutWindows(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	childPIDPath := filepath.Join(tempDir, "child.pid")

	adapter := NewGenericCLIRunnerAdapter("custom-cli", os.Args[0], []string{
		"-test.run=^TestManagedSupervisorWindowsHelper$",
		"--",
		"blocked-parent",
		childPIDPath,
	}, nil)
	adapter.gracePeriod = 50 * time.Millisecond

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-process-job-timeout",
		RepoRoot: tempDir,
		Timeout:  40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status from timeout, got %q (%s)", result.Status, result.Reason)
	}
	if !strings.Contains(result.Reason, "runner timeout") {
		t.Fatalf("expected runner timeout reason, got %q", result.Reason)
	}

	assertProcessEventuallyStopped(t, waitForWindowsPIDFile(t, childPIDPath))
}

func TestGenericCLIRunnerAdapterManagedSupervisorCleansUpSpawnedChildrenOnCancellationWindows(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	childPIDPath := filepath.Join(tempDir, "child.pid")

	adapter := NewGenericCLIRunnerAdapter("custom-cli", os.Args[0], []string{
		"-test.run=^TestManagedSupervisorWindowsHelper$",
		"--",
		"blocked-parent",
		childPIDPath,
	}, nil)
	adapter.gracePeriod = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		result contracts.RunnerResult
		err    error
	}, 1)
	go func() {
		result, err := adapter.Run(ctx, contracts.RunnerRequest{
			TaskID:   "task-process-job-cancel",
			RepoRoot: tempDir,
		})
		done <- struct {
			result contracts.RunnerResult
			err    error
		}{result: result, err: err}
	}()

	childPID := waitForWindowsPIDFile(t, childPIDPath)
	cancel()

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("run failed: %v", outcome.err)
		}
		if outcome.result.Status != contracts.RunnerResultFailed {
			t.Fatalf("expected failed status from cancellation, got %q (%s)", outcome.result.Status, outcome.result.Reason)
		}
		if !strings.Contains(outcome.result.Reason, context.Canceled.Error()) {
			t.Fatalf("expected cancellation reason, got %q", outcome.result.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected managed run to return after cancellation")
	}

	assertProcessEventuallyStopped(t, childPID)
}

func TestManagedSupervisorWindowsHelper(t *testing.T) {
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || len(args) <= sep+2 {
		return
	}

	mode := args[sep+1]
	childPIDPath := args[sep+2]

	switch mode {
	case "complete-parent":
		startWindowsHelperChild(childPIDPath)
		time.Sleep(75 * time.Millisecond)
		os.Exit(0)
	case "blocked-parent":
		startWindowsHelperChild(childPIDPath)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		defer signal.Stop(sigCh)
		select {
		case <-sigCh:
			os.Exit(0)
		case <-time.After(5 * time.Second):
			os.Exit(3)
		}
	case "child":
		if err := os.WriteFile(childPIDPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			os.Exit(4)
		}
		for {
			time.Sleep(time.Second)
		}
	}
}

func startWindowsHelperChild(childPIDPath string) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestManagedSupervisorWindowsHelper$", "--", "child", childPIDPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		os.Exit(2)
	}
}

func waitForWindowsPIDFile(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		content, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(content)))
			if convErr != nil {
				t.Fatalf("parse pid file %s: %v", path, convErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read pid file %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for pid file %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertProcessEventuallyStopped(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for processAliveWindows(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("expected child process %d to be cleaned up with parent backend", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processAliveWindows(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	status, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && status == uint32(windows.WAIT_TIMEOUT)
}
