package opencode

import (
	"context"
	"encoding/json"
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

func TestRedactArgsForRun(t *testing.T) {
	args := []string{"opencode", "run", "secret prompt", "--agent", "yolo"}

	redacted := RedactArgs(args)
	if redacted[2] != "<prompt redacted>" {
		t.Fatalf("expected prompt redacted, got %q", redacted[2])
	}
	if args[2] != "secret prompt" {
		t.Fatalf("expected original args unchanged")
	}
}

func TestBuildEnvAddsDisableFlagsAndCI(t *testing.T) {
	env := BuildEnv(map[string]string{"HELLO": "world"}, "", "", "")

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

func TestBuildEnvSetsModelConfigContent(t *testing.T) {
	tempDir := t.TempDir()
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")

	env := BuildEnv(nil, configRoot, configDir, "zai-coding-plan/glm-4.7")
	if env["OPENCODE_CONFIG_CONTENT"] != "{\"model\":\"zai-coding-plan/glm-4.7\"}" {
		t.Fatalf("unexpected config content: %q", env["OPENCODE_CONFIG_CONTENT"])
	}
}

func TestBuildEnvSetsDeterministicPermissionPolicy(t *testing.T) {
	env := BuildEnv(nil, "", "", "")
	raw := strings.TrimSpace(env["OPENCODE_PERMISSION"])
	if raw == "" {
		t.Fatalf("expected OPENCODE_PERMISSION to be set")
	}

	policy := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		t.Fatalf("decode OPENCODE_PERMISSION: %v", err)
	}
	for _, key := range []string{"*", "doom_loop", "external_directory", "question", "plan_enter", "plan_exit"} {
		if policy[key] != "allow" {
			t.Fatalf("expected %s=allow, got %#v", key, policy)
		}
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

	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		return nil
	})
	if err := RunWithACP(
		context.Background(),
		"issue-1",
		"/repo",
		"prompt",
		"",
		configRoot,
		configDir,
		logPath,
		runner,
		acpClient,
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

	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		return nil
	})
	if err := RunWithACP(
		context.Background(),
		"issue-99",
		repoRoot,
		"prompt",
		"",
		configRoot,
		configDir,
		"",
		runner,
		acpClient,
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

func TestRunWithACPContextCancelsProcess(t *testing.T) {
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
	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	go func() {
		done <- RunWithACP(ctx, "issue-1", "/repo", "prompt", "", configRoot, configDir, logPath, runner, acpClient)
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

func TestRunWithACPWaitsForProcessExit(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")
	proc := newDelayedProcess()
	started := make(chan struct{})
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		close(started)
		return proc, nil
	})

	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		return nil
	})

	go func() {
		<-started
		time.Sleep(50 * time.Millisecond)
		proc.finish(nil)
	}()

	if err := RunWithACP(context.Background(), "issue-1", tempDir, "prompt", "", "", "", logPath, runner, acpClient); err != nil {
		t.Fatalf("RunWithACP error: %v", err)
	}
	if proc.killed {
		t.Fatalf("expected process not to be killed")
	}
}

func TestRunWithACPRequiresStdioProcessForDefaultClient(t *testing.T) {
	tempDir := t.TempDir()
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})

	err := RunWithACP(context.Background(), "issue-1", tempDir, "prompt", "", configRoot, configDir, logPath, runner, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "stdin/stdout") {
		t.Fatalf("expected stdin/stdout error, got %v", err)
	}
}

func TestRunUsesACPClient(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	called := make(chan struct{}, 1)
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})
	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		called <- struct{}{}
		return nil
	})
	if err := RunWithACP(context.Background(), "issue-1", repoRoot, "prompt", "", "", "", logPath, runner, acpClient); err != nil {
		t.Fatalf("RunWithACP error: %v", err)
	}
	select {
	case <-called:
	default:
		t.Fatalf("expected ACP client to be called")
	}
}

func TestRunWithACPIncludesModelInConfigContent(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	var capturedArgs []string
	var capturedEnv map[string]string
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		capturedArgs = append([]string{}, args...)
		capturedEnv = map[string]string{}
		for key, value := range env {
			capturedEnv[key] = value
		}
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})
	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		return nil
	})
	if err := RunWithACP(context.Background(), "issue-1", repoRoot, "prompt", "zai-coding-plan/glm-4.7", configRoot, configDir, logPath, runner, acpClient); err != nil {
		t.Fatalf("RunWithACP error: %v", err)
	}

	if capturedEnv["OPENCODE_CONFIG_CONTENT"] != "{\"model\":\"zai-coding-plan/glm-4.7\"}" {
		t.Fatalf("expected model in config content, got %q", capturedEnv["OPENCODE_CONFIG_CONTENT"])
	}
	for _, arg := range capturedArgs {
		if arg == "--model" {
			t.Fatalf("did not expect --model in args: %v", capturedArgs)
		}
	}
}

func TestRunWithACPNoModelWhenEmpty(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	var capturedArgs []string
	var capturedEnv map[string]string
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		capturedArgs = append([]string{}, args...)
		capturedEnv = map[string]string{}
		for key, value := range env {
			capturedEnv[key] = value
		}
		proc := newFakeProcess()
		close(proc.waitCh)
		return proc, nil
	})
	acpClient := ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		return nil
	})
	if err := RunWithACP(context.Background(), "issue-1", repoRoot, "prompt", "", configRoot, configDir, logPath, runner, acpClient); err != nil {
		t.Fatalf("RunWithACP error: %v", err)
	}

	// Check that --model is NOT in the args when model is empty
	for _, arg := range capturedArgs {
		if arg == "--model" {
			t.Fatalf("did not expect --model in args when model is empty: %v", capturedArgs)
		}
	}
	if capturedEnv["OPENCODE_CONFIG_CONTENT"] != "{}" {
		t.Fatalf("expected empty config content, got %q", capturedEnv["OPENCODE_CONFIG_CONTENT"])
	}
}

func TestRunWithACPUsesWatchdogAndClassifiesPermissionStall(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-permission.jsonl")

	homeDir := filepath.Join(tempDir, "home")
	logDir := filepath.Join(homeDir, ".local", "share", "opencode", "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir opencode log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "latest.log"), []byte("INFO service=permission permission=ask sessionID=ses_perm\n"), 0o644); err != nil {
		t.Fatalf("write opencode log: %v", err)
	}
	defaultHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { defaultHomeDir = os.UserHomeDir })

	proc := &MockProcess{waitCh: make(chan error), killCh: make(chan error, 1), stdin: &nopWriteCloser{}, stdout: &nopReadCloser{}}
	proc.killCh <- nil
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		if err := os.WriteFile(stdoutPath, []byte(""), 0o644); err != nil {
			return nil, err
		}
		return proc, nil
	})

	ctx := withWatchdogRuntimeConfig(context.Background(), watchdogRuntimeConfig{Timeout: 40 * time.Millisecond, Interval: 5 * time.Millisecond})
	err := RunWithACP(ctx, "issue-permission", repoRoot, "prompt", "", "", "", logPath, runner, ACPClientFunc(func(context.Context, string, string) error {
		return nil
	}))
	if err == nil {
		t.Fatalf("expected watchdog stall error")
	}
	var stallErr *StallError
	if !errors.As(err, &stallErr) {
		t.Fatalf("expected StallError, got %T (%v)", err, err)
	}
	if stallErr.Category != stallPermission {
		t.Fatalf("expected %q category, got %q", stallPermission, stallErr.Category)
	}
	if !proc.killCalled {
		t.Fatalf("expected process to be killed by watchdog")
	}
}

func TestRunWithACPUsesWatchdogAndClassifiesQuestionStall(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-question.jsonl")

	homeDir := filepath.Join(tempDir, "home")
	logDir := filepath.Join(homeDir, ".local", "share", "opencode", "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir opencode log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "latest.log"), []byte("INFO service=question text=Need user decision\n"), 0o644); err != nil {
		t.Fatalf("write opencode log: %v", err)
	}
	defaultHomeDir = func() (string, error) { return homeDir, nil }
	t.Cleanup(func() { defaultHomeDir = os.UserHomeDir })

	proc := &MockProcess{waitCh: make(chan error), killCh: make(chan error, 1), stdin: &nopWriteCloser{}, stdout: &nopReadCloser{}}
	proc.killCh <- nil
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		if err := os.WriteFile(stdoutPath, []byte(""), 0o644); err != nil {
			return nil, err
		}
		return proc, nil
	})

	ctx := withWatchdogRuntimeConfig(context.Background(), watchdogRuntimeConfig{Timeout: 40 * time.Millisecond, Interval: 5 * time.Millisecond})
	err := RunWithACP(ctx, "issue-question", repoRoot, "prompt", "", "", "", logPath, runner, ACPClientFunc(func(context.Context, string, string) error {
		return nil
	}))
	if err == nil {
		t.Fatalf("expected watchdog stall error")
	}
	var stallErr *StallError
	if !errors.As(err, &stallErr) {
		t.Fatalf("expected StallError, got %T (%v)", err, err)
	}
	if stallErr.Category != stallQuestion {
		t.Fatalf("expected %q category, got %q", stallQuestion, stallErr.Category)
	}
	if !proc.killCalled {
		t.Fatalf("expected process to be killed by watchdog")
	}
}

func TestRunWithACPTreatsIdleTransportMarkersAsCompletedRun(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-idle-complete.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir runner log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write runner log: %v", err)
	}
	oldTime := time.Now().Add(-30 * time.Second)
	if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}
	stderrPath := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	if err := os.WriteFile(stderrPath, []byte("INFO service=session.prompt sessionID=ses_idle exiting loop\nINFO service=session.prompt sessionID=ses_idle cancel\nINFO service=bus type=session.idle publishing\n"), 0o644); err != nil {
		t.Fatalf("write stderr log: %v", err)
	}

	proc := &MockProcess{waitCh: make(chan error), killCh: make(chan error, 1), stdin: &nopWriteCloser{}, stdout: &nopReadCloser{}}
	proc.killCh <- nil
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	})

	runtimeCtx := withWatchdogRuntimeConfig(context.Background(), watchdogRuntimeConfig{Timeout: 10 * time.Minute, Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithTimeout(runtimeCtx, 2*time.Second)
	defer cancel()

	err := RunWithACP(ctx, "issue-idle-complete", repoRoot, "prompt", "", "", "", logPath, runner, ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatalf("expected idle markers to complete run, got %v", err)
	}
	if !proc.killCalled {
		t.Fatalf("expected process kill for idle-transport cleanup")
	}
}

func TestRunWithACPTreatsPermissionStallAsCompletedWhenPromptLoopEnded(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-permission-idle.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir runner log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write runner log: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(logPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}
	stderrPath := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	if err := os.WriteFile(stderrPath, []byte("INFO service=session.prompt sessionID=ses_idle exiting loop\nINFO service=session.prompt sessionID=ses_idle cancel\nINFO service=bus type=session.idle publishing\n"), 0o644); err != nil {
		t.Fatalf("write stderr log: %v", err)
	}

	logDir := filepath.Join(tempDir, "opencode", "log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir opencode log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "latest.log"), []byte("INFO service=permission permission=ask sessionID=ses_perm\n"), 0o644); err != nil {
		t.Fatalf("write opencode log: %v", err)
	}

	proc := &MockProcess{waitCh: make(chan error), killCh: make(chan error, 1), stdin: &nopWriteCloser{}, stdout: &nopReadCloser{}}
	proc.killCh <- nil
	runner := RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		if err := os.WriteFile(stdoutPath, []byte(""), 0o644); err != nil {
			return nil, err
		}
		return proc, nil
	})

	runtimeCtx := withWatchdogRuntimeConfig(context.Background(), watchdogRuntimeConfig{Timeout: 40 * time.Millisecond, Interval: 5 * time.Millisecond, OpenCodeLogDir: logDir})
	ctx, cancel := context.WithTimeout(runtimeCtx, 2*time.Second)
	defer cancel()

	err := RunWithACP(ctx, "issue-permission-idle", repoRoot, "prompt", "", "", "", logPath, runner, ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatalf("expected prompt-loop markers to override permission stall, got %v", err)
	}
	if !proc.killCalled {
		t.Fatalf("expected process to be killed")
	}
}

func TestForwardACPRequestLineForUpdates(t *testing.T) {
	seen := []string{}
	line := forwardACPRequestLine("permission", "allow", "read /tmp/file.txt", func(update string) {
		seen = append(seen, update)
	})
	if line != "request permission allow detail=\"read /tmp/file.txt\"" {
		t.Fatalf("unexpected request line: %q", line)
	}
	if len(seen) != 1 || seen[0] != line {
		t.Fatalf("expected permission request forwarded once, got %#v", seen)
	}
}
