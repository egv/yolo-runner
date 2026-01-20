package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestStartCommandWithEnvSeparatesStdoutAndStderr(t *testing.T) {
	tempDir := t.TempDir()
	stdoutPath := filepath.Join(tempDir, "output.jsonl")
	args := []string{"/bin/sh", "-c", "printf '{\"ok\":true}\\n'; printf 'stderr-line\\n' 1>&2"}

	process, err := startCommandWithEnv(args, nil, stdoutPath)
	if err != nil {
		t.Fatalf("startCommandWithEnv error: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("process wait error: %v", err)
	}

	stdoutContent, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if string(stdoutContent) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected stdout log: %q", string(stdoutContent))
	}

	stderrPath := strings.TrimSuffix(stdoutPath, ".jsonl") + ".stderr.log"
	stderrContent, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr log: %v", err)
	}
	if string(stderrContent) != "stderr-line\n" {
		t.Fatalf("unexpected stderr log: %q", string(stderrContent))
	}
}

func TestRunCommandPrintsPhaseMessages(t *testing.T) {
	buffer := &bytes.Buffer{}
	prevOutput := commandOutput
	t.Cleanup(func() { commandOutput = prevOutput })
	commandOutput = buffer

	_, err := runCommand("/bin/sh", "-c", "printf 'ok' > /dev/null")
	if err != nil {
		t.Fatalf("runCommand error: %v", err)
	}

	printed := buffer.String()
	if !strings.Contains(printed, "$ /bin/sh -c printf 'ok' > /dev/null") {
		t.Fatalf("expected command echo, got %q", printed)
	}
	if !regexp.MustCompile(`(?m)^ok \(exit=0, elapsed=\d+ms\)$`).MatchString(printed) {
		t.Fatalf("expected ok line with exit and elapsed, got %q", printed)
	}
}

func TestRunCommandPrintsFailures(t *testing.T) {
	buffer := &bytes.Buffer{}
	prevOutput := commandOutput
	t.Cleanup(func() { commandOutput = prevOutput })
	commandOutput = buffer

	_, err := runCommand("/bin/sh", "-c", "exit 2")
	if err == nil {
		t.Fatalf("expected error")
	}

	printed := buffer.String()
	if !strings.Contains(printed, "$ /bin/sh -c exit 2") {
		t.Fatalf("expected command echo, got %q", printed)
	}
	if !regexp.MustCompile(`(?m)^failed \(exit=2, elapsed=\d+ms\)$`).MatchString(printed) {
		t.Fatalf("expected failed line with exit and elapsed, got %q", printed)
	}
}

func TestStartCommandWithEnvPrintsPhaseMessages(t *testing.T) {
	buffer := &bytes.Buffer{}
	prevOutput := commandOutput
	t.Cleanup(func() { commandOutput = prevOutput })
	commandOutput = buffer

	tempDir := t.TempDir()
	stdoutPath := filepath.Join(tempDir, "output.jsonl")
	args := []string{"/bin/sh", "-c", "printf 'ok'"}

	process, err := startCommandWithEnv(args, nil, stdoutPath)
	if err != nil {
		t.Fatalf("startCommandWithEnv error: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("process wait error: %v", err)
	}

	printed := buffer.String()
	if !strings.Contains(printed, "$ /bin/sh -c printf 'ok'") {
		t.Fatalf("expected command echo, got %q", printed)
	}
	if !regexp.MustCompile(`(?m)^ok \(exit=0, elapsed=\d+ms\)$`).MatchString(printed) {
		t.Fatalf("expected ok line with exit and elapsed, got %q", printed)
	}
}
