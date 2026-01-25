package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildArgsWithoutModel(t *testing.T) {
	args := BuildArgs("/repo", "prompt", "")

	if strings.Join(args, " ") != "opencode run prompt --agent yolo --format json /repo" {
		t.Fatalf("unexpected args: %v", args)
	}

	for _, arg := range args {
		if arg == "--model" {
			t.Fatalf("did not expect --model in args: %v", args)
		}
	}
}

func TestBuildArgsWithModel(t *testing.T) {
	args := BuildArgs("/repo", "prompt", "gpt-4o")

	expected := []string{"opencode", "run", "prompt", "--agent", "yolo", "--format", "json", "--model", "gpt-4o", "/repo"}
	if strings.Join(args, " ") != strings.Join(expected, " ") {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestBuildEnvAddsDisableFlagsAndCI(t *testing.T) {
	env := BuildEnv(map[string]string{"HELLO": "world"}, "", "")

	if env["CI"] != "true" {
		t.Fatalf("expected CI true, got %q", env["CI"])
	}
	if env["OPENCODE_DISABLE_CLAUDE_CODE"] != "true" {
		t.Fatalf("expected OPENCODE_DISABLE_CLAUDE_CODE true")
	}
	if env["OPENCODE_DISABLE_CLAUDE_CODE_SKILLS"] != "true" {
		t.Fatalf("expected OPENCODE_DISABLE_CLAUDE_CODE_SKILLS true")
	}
	if env["OPENCODE_DISABLE_CLAUDE_CODE_PROMPT"] != "true" {
		t.Fatalf("expected OPENCODE_DISABLE_CLAUDE_CODE_PROMPT true")
	}
	if env["OPENCODE_DISABLE_DEFAULT_PLUGINS"] != "true" {
		t.Fatalf("expected OPENCODE_DISABLE_DEFAULT_PLUGINS true")
	}
	if env["HELLO"] != "world" {
		t.Fatalf("expected base env preserved")
	}
}

func TestRunEnsuresConfigAndOverwritesLog(t *testing.T) {
	tempDir := t.TempDir()
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale log: %v", err)
	}

	var capturedArgs []string
	var capturedEnv map[string]string
	var capturedPath string

	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		capturedArgs = append([]string{}, args...)
		capturedEnv = make(map[string]string)
		for key, value := range env {
			capturedEnv[key] = value
		}
		capturedPath = stdoutPath
		if err := os.WriteFile(stdoutPath, []byte("{\"ok\":true}\n"), 0o644); err != nil {
			return nil, err
		}
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	defaultHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { defaultHomeDir = os.UserHomeDir })

	if err := Run(
		"issue-1",
		"/repo",
		"prompt",
		"",
		configRoot,
		configDir,
		logPath,
		runner,
	); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if len(capturedArgs) == 0 {
		t.Fatalf("expected runner to be called")
	}

	if capturedPath != logPath {
		t.Fatalf("expected log path %q, got %q", logPath, capturedPath)
	}

	if _, err := os.Stat(configDir); err != nil {
		t.Fatalf("expected config dir to exist: %v", err)
	}

	if _, err := os.Stat(filepath.Join(configDir, "opencode.json")); err != nil {
		t.Fatalf("expected opencode.json to exist: %v", err)
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
	if string(content) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected log content: %q", string(content))
	}

	if capturedEnv["OPENCODE_CONFIG_DIR"] != configDir {
		t.Fatalf("expected OPENCODE_CONFIG_DIR set")
	}
	if capturedEnv["OPENCODE_CONFIG"] != filepath.Join(configDir, "opencode.json") {
		t.Fatalf("expected OPENCODE_CONFIG set")
	}
	if capturedEnv["OPENCODE_CONFIG_CONTENT"] != "{}" {
		t.Fatalf("expected OPENCODE_CONFIG_CONTENT set")
	}
	if capturedEnv["XDG_CONFIG_HOME"] != configRoot {
		t.Fatalf("expected XDG_CONFIG_HOME set")
	}
}

func TestRunDefaultsLogPath(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")

	var capturedPath string
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		capturedPath = stdoutPath
		if err := os.WriteFile(stdoutPath, []byte("{\"ok\":true}\n"), 0o644); err != nil {
			return nil, err
		}
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})

	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	defaultHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { defaultHomeDir = os.UserHomeDir })

	if err := Run(
		"issue-99",
		repoRoot,
		"prompt",
		"",
		configRoot,
		configDir,
		"",
		runner,
	); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	expectedPath := filepath.Join(repoRoot, "runner-logs", "opencode", "issue-99.jsonl")
	if capturedPath != expectedPath {
		t.Fatalf("expected log path %q, got %q", expectedPath, capturedPath)
	}

	content, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
	if string(content) != "{\"ok\":true}\n" {
		t.Fatalf("unexpected log content: %q", string(content))
	}
}

func TestRunWithContextCancelsProcess(t *testing.T) {
	tempDir := t.TempDir()
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	proc := newFakeProcess()
	started := make(chan struct{})
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		close(started)
		return proc, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunWithContext(ctx, "issue-1", "/repo", "prompt", "", configRoot, configDir, logPath, runner)
	}()

	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected RunWithContext to return")
	}

	if !proc.killed {
		t.Fatalf("expected process to be killed")
	}
}

func TestRunUsesACPClient(t *testing.T) {
	called := false
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})
	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		called = true
		return nil
	})
	if err := RunWithACP(context.Background(), "issue-1", "/repo", "prompt", "", "", "", "", runner, acpClient); err != nil {
		t.Fatalf("RunWithACP error: %v", err)
	}
	if !called {
		t.Fatalf("expected ACP client to be called")
	}
}
