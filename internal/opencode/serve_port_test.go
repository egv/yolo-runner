package opencode

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestAllocateServePortIsDeterministicForSameRequest(t *testing.T) {
	request := contracts.TaskSessionStartRequest{
		TaskID:   "task-1",
		RepoRoot: "/tmp/repo",
		LogPath:  "/tmp/repo/runner-logs/opencode/task-1.jsonl",
	}

	first, err := AllocateServePort(defaultServeHostname, request)
	if err != nil {
		t.Fatalf("allocate first port: %v", err)
	}
	second, err := AllocateServePort(defaultServeHostname, request)
	if err != nil {
		t.Fatalf("allocate second port: %v", err)
	}

	if first != second {
		t.Fatalf("expected deterministic port allocation, got %d then %d", first, second)
	}
}

func TestAllocateServePortSkipsOccupiedPortDeterministically(t *testing.T) {
	request := contracts.TaskSessionStartRequest{
		TaskID:   "task-occupied",
		RepoRoot: "/tmp/repo",
		LogPath:  "/tmp/repo/runner-logs/opencode/task-occupied.jsonl",
	}

	preferredPort, err := AllocateServePort(defaultServeHostname, request)
	if err != nil {
		t.Fatalf("allocate preferred port: %v", err)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(defaultServeHostname, strconv.Itoa(preferredPort)))
	if err != nil {
		t.Fatalf("listen on preferred port %d: %v", preferredPort, err)
	}
	defer func() {
		_ = listener.Close()
	}()

	fallbackPort, err := AllocateServePort(defaultServeHostname, request)
	if err != nil {
		t.Fatalf("allocate fallback port: %v", err)
	}
	repeatedFallbackPort, err := AllocateServePort(defaultServeHostname, request)
	if err != nil {
		t.Fatalf("allocate repeated fallback port: %v", err)
	}

	if fallbackPort == preferredPort {
		t.Fatalf("expected occupied preferred port %d to be skipped", preferredPort)
	}
	if repeatedFallbackPort != fallbackPort {
		t.Fatalf("expected deterministic fallback port, got %d then %d", fallbackPort, repeatedFallbackPort)
	}
}

func TestTaskSessionRuntimeStartUsesDeterministicPortAllocator(t *testing.T) {
	runtime := NewTaskSessionRuntime("opencode")
	request := contracts.TaskSessionStartRequest{
		TaskID:   "task-deterministic-port",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-deterministic-port.jsonl"),
	}

	var started []ServeCommandSpec
	processes := make([]*fakeServeProcess, 0, 2)
	runtime.starter = serveProcessStarterFunc(func(_ context.Context, spec ServeCommandSpec) (serveProcess, error) {
		started = append(started, spec)
		proc := newFakeServeProcess()
		processes = append(processes, proc)
		return proc, nil
	})

	sessionOne, err := runtime.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("start first session: %v", err)
	}
	sessionTwo, err := runtime.Start(context.Background(), request)
	if err != nil {
		t.Fatalf("start second session: %v", err)
	}

	if len(started) != 2 {
		t.Fatalf("expected two started specs, got %d", len(started))
	}

	firstPort := portFromServeArgs(t, started[0].Args)
	secondPort := portFromServeArgs(t, started[1].Args)
	if firstPort != secondPort {
		t.Fatalf("expected same deterministic serve port across starts, got %d then %d", firstPort, secondPort)
	}

	processes[0].waitCh <- nil
	processes[1].waitCh <- nil
	sessionOne.(*ServeTaskSession).closeLogs()
	sessionTwo.(*ServeTaskSession).closeLogs()
}

func portFromServeArgs(t *testing.T, args []string) int {
	t.Helper()

	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--port" {
			continue
		}
		port, err := strconv.Atoi(args[i+1])
		if err != nil {
			t.Fatalf("parse port %q: %v", args[i+1], err)
		}
		return port
	}

	t.Fatalf("missing --port in args %#v", args)
	return 0
}
