package opencode

import (
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestTaskSessionRuntimeBuildCommandUsesServeLoopbackHostByDefault(t *testing.T) {
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode")

	command := runtime.buildCommand(contracts.TaskSessionStartRequest{})
	expected := []string{"/tmp/custom-opencode", "serve", "--print-logs", "--log-level", "DEBUG", "--hostname", "127.0.0.1"}
	if len(command) != len(expected) {
		t.Fatalf("expected %d command args, got %d: %#v", len(expected), len(command), command)
	}
	for i, want := range expected {
		if command[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, command[i])
		}
	}
}

func TestTaskSessionRuntimeBuildCommandAddsLoopbackHostToConfiguredServeCommand(t *testing.T) {
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode", "serve", "--print-logs", "--log-level", "DEBUG")

	command := runtime.buildCommand(contracts.TaskSessionStartRequest{})
	expected := []string{"/tmp/custom-opencode", "serve", "--print-logs", "--log-level", "DEBUG", "--hostname", "127.0.0.1"}
	if len(command) != len(expected) {
		t.Fatalf("expected %d command args, got %d: %#v", len(expected), len(command), command)
	}
	for i, want := range expected {
		if command[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, command[i])
		}
	}
}

func TestTaskSessionRuntimeBuildCommandNormalizesRequestServeCommandToConfiguredBinary(t *testing.T) {
	runtime := NewTaskSessionRuntime("/tmp/custom-opencode")

	command := runtime.buildCommand(contracts.TaskSessionStartRequest{
		Command: []string{"opencode", "serve", "--print-logs", "--log-level", "DEBUG"},
	})
	expected := []string{"/tmp/custom-opencode", "serve", "--print-logs", "--log-level", "DEBUG", "--hostname", "127.0.0.1"}
	if len(command) != len(expected) {
		t.Fatalf("expected %d command args, got %d: %#v", len(expected), len(command), command)
	}
	for i, want := range expected {
		if command[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, command[i])
		}
	}
}
