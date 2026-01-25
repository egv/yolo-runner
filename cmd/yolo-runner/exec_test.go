package main

import (
	"bytes"
	"io"
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

	stdio, ok := process.(interface {
		Stdout() io.ReadCloser
	})
	if !ok {
		_ = process.Kill()
		_ = process.Wait()
		t.Fatalf("expected process to expose stdout")
	}

	stdoutContent, err := io.ReadAll(stdio.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("process wait error: %v", err)
	}
	if string(stdoutContent) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected stdout: %q", string(stdoutContent))
	}

	stderrPath := strings.TrimSuffix(stdoutPath, ".jsonl") + ".stderr.log"
	stderrContent, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr log: %v", err)
	}
	if string(stderrContent) != "stderr-line\n" {
		t.Fatalf("unexpected stderr log: %q", string(stderrContent))
	}

	fileContent, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if len(fileContent) != 0 {
		t.Fatalf("expected stdout log file to be empty, got %q", string(fileContent))
	}
}

func TestStartCommandWithEnvExposesStdio(t *testing.T) {
	tempDir := t.TempDir()
	stdoutPath := filepath.Join(tempDir, "output.jsonl")
	args := []string{"/bin/cat"}

	process, err := startCommandWithEnv(args, nil, stdoutPath)
	if err != nil {
		t.Fatalf("startCommandWithEnv error: %v", err)
	}

	stdio, ok := process.(interface {
		Stdin() io.WriteCloser
		Stdout() io.ReadCloser
	})
	if !ok {
		_ = process.Kill()
		_ = process.Wait()
		t.Fatalf("expected process to expose stdin/stdout")
	}

	if _, err := io.WriteString(stdio.Stdin(), "hello\n"); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = stdio.Stdin().Close()

	output, err := io.ReadAll(stdio.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if string(output) != "hello\n" {
		t.Fatalf("unexpected stdout: %q", string(output))
	}

	if err := process.Wait(); err != nil {
		t.Fatalf("process wait error: %v", err)
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

func TestPrintCommandRedactsOpenCodePrompt(t *testing.T) {
	buffer := &bytes.Buffer{}
	prompt := "Large prompt with description and acceptance criteria"
	args := []string{"opencode", "run", prompt, "--agent", "yolo", "--format", "json", "."}

	printCommand(buffer, args)

	printed := buffer.String()
	if strings.Contains(printed, prompt) {
		t.Fatalf("expected prompt redacted, got %q", printed)
	}
	if !strings.Contains(printed, "$ opencode run <prompt redacted> --agent yolo --format json .") {
		t.Fatalf("expected redacted command echo, got %q", printed)
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
