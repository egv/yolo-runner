package yolo_runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildOpenCodeArgsWithoutModel(t *testing.T) {
	args := buildOpenCodeArgs("/repo", "prompt", "")

	if strings.Join(args, " ") != "opencode run prompt --agent yolo --format json /repo" {
		t.Fatalf("unexpected args: %v", args)
	}

	for _, arg := range args {
		if arg == "--model" {
			t.Fatalf("did not expect --model in args: %v", args)
		}
	}
}

func TestBuildOpenCodeArgsWithModel(t *testing.T) {
	args := buildOpenCodeArgs("/repo", "prompt", "gpt-4o")

	expected := []string{"opencode", "run", "prompt", "--agent", "yolo", "--format", "json", "--model", "gpt-4o", "/repo"}
	if strings.Join(args, " ") != strings.Join(expected, " ") {
		t.Fatalf("unexpected args: %v", args)
	}
}

func TestBuildOpenCodeEnvAddsDisableFlagsAndCI(t *testing.T) {
	env := buildOpenCodeEnv(map[string]string{"HELLO": "world"}, "", "")

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

func TestRunOpenCodeEnsuresConfigAndLogs(t *testing.T) {
	tempDir := t.TempDir()
	configRoot := filepath.Join(tempDir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	var capturedArgs []string
	var capturedEnv map[string]string

	runner := func(args []string, env map[string]string, stdoutPath string) error {
		capturedArgs = append([]string{}, args...)
		capturedEnv = make(map[string]string)
		for key, value := range env {
			capturedEnv[key] = value
		}
		if err := os.WriteFile(stdoutPath, []byte("{\"ok\":true}\n"), 0o644); err != nil {
			return err
		}
		return nil
	}

	if err := runOpenCode(
		"issue-1",
		"/repo",
		"prompt",
		"",
		configRoot,
		configDir,
		logPath,
		runner,
	); err != nil {
		t.Fatalf("runOpenCode error: %v", err)
	}

	if len(capturedArgs) == 0 {
		t.Fatalf("expected runner to be called")
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
}
