package main

import (
	"os"
	"path/filepath"
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
