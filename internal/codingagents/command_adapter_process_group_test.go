//go:build !windows

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
	"syscall"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestGenericCLIRunnerAdapterManagedSupervisorCleansUpSpawnedChildrenOnTimeout(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	childPIDPath := filepath.Join(tempDir, "child.pid")

	adapter := NewGenericCLIRunnerAdapter("custom-cli", os.Args[0], []string{
		"-test.run=^TestManagedSupervisorProcessGroupHelper$",
		"--",
		"parent",
		childPIDPath,
	}, nil).WithHealthConfig(&BackendHealthConfig{
		Enabled:  true,
		Command:  "false",
		Timeout:  "5ms",
		Interval: "5ms",
	})
	adapter.gracePeriod = 50 * time.Millisecond

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-process-group-timeout",
		RepoRoot: tempDir,
		Timeout:  30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status from readiness timeout, got %q (%s)", result.Status, result.Reason)
	}
	if !strings.Contains(result.Reason, "runner timeout") {
		t.Fatalf("expected runner timeout reason, got %q", result.Reason)
	}

	childPID := waitForPIDFile(t, childPIDPath)
	t.Cleanup(func() {
		if processAlive(childPID) {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for processAlive(childPID) {
		if time.Now().After(deadline) {
			t.Fatalf("expected child process %d to be cleaned up with parent backend", childPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagedSupervisorProcessGroupHelper(t *testing.T) {
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
	case "parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestManagedSupervisorProcessGroupHelper$", "--", "child", childPIDPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
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

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
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

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}
