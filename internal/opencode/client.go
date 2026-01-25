package opencode

import (
	"context"
	"os"
	"path/filepath"
)

type Runner interface {
	Start(args []string, env map[string]string, stdoutPath string) (Process, error)
}

type RunnerFunc func(args []string, env map[string]string, stdoutPath string) (Process, error)

func (runner RunnerFunc) Start(args []string, env map[string]string, stdoutPath string) (Process, error) {
	return runner(args, env, stdoutPath)
}

type ACPClient interface {
	Run(ctx context.Context, issueID string, logPath string) error
}

type ACPClientFunc func(ctx context.Context, issueID string, logPath string) error

func (client ACPClientFunc) Run(ctx context.Context, issueID string, logPath string) error {
	return client(ctx, issueID, logPath)
}

func BuildArgs(repoRoot string, prompt string, model string) []string {
	args := []string{"opencode", "run", prompt, "--agent", "yolo", "--format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, repoRoot)
	return args
}

func BuildEnv(baseEnv map[string]string, configRoot string, configDir string) map[string]string {
	env := map[string]string{}
	if baseEnv != nil {
		for key, value := range baseEnv {
			env[key] = value
		}
	}
	env["OPENCODE_DISABLE_CLAUDE_CODE"] = "true"
	env["OPENCODE_DISABLE_CLAUDE_CODE_SKILLS"] = "true"
	env["OPENCODE_DISABLE_CLAUDE_CODE_PROMPT"] = "true"
	env["OPENCODE_DISABLE_DEFAULT_PLUGINS"] = "true"
	env["CI"] = "true"

	if configRoot != "" {
		_ = os.MkdirAll(configRoot, 0o755)
		env["XDG_CONFIG_HOME"] = configRoot
	}

	if configDir != "" {
		_ = os.MkdirAll(configDir, 0o755)
		configFile := filepath.Join(configDir, "opencode.json")
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			_ = os.WriteFile(configFile, []byte("{}"), 0o644)
		}
		env["OPENCODE_CONFIG_DIR"] = configDir
		env["OPENCODE_CONFIG"] = configFile
		env["OPENCODE_CONFIG_CONTENT"] = "{}"
	}

	return env
}

func Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner) error {
	if runner == nil {
		return nil
	}
	if configRoot != "" {
		if err := os.MkdirAll(configRoot, 0o755); err != nil {
			return err
		}
	}
	if configDir != "" {
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return err
		}
		configFile := filepath.Join(configDir, "opencode.json")
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			if err := os.WriteFile(configFile, []byte("{}"), 0o644); err != nil {
				return err
			}
		}
	}
	if logPath == "" {
		logPath = filepath.Join(repoRoot, "runner-logs", "opencode", issueID+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}

	args := BuildArgs(repoRoot, prompt, model)
	env := BuildEnv(nil, configRoot, configDir)
	process, err := runner.Start(args, env, logPath)
	if err != nil {
		return err
	}
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:        logPath,
		OpenCodeLogDir: filepath.Join(os.Getenv("HOME"), ".local", "share", "opencode", "log"),
		TailLines:      50,
	})
	if err := watchdog.Monitor(process); err != nil {
		return err
	}
	return nil
}

func RunWithContext(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		return nil
	}
	if configRoot != "" {
		if err := os.MkdirAll(configRoot, 0o755); err != nil {
			return err
		}
	}
	if configDir != "" {
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return err
		}
		configFile := filepath.Join(configDir, "opencode.json")
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			if err := os.WriteFile(configFile, []byte("{}"), 0o644); err != nil {
				return err
			}
		}
	}
	if logPath == "" {
		logPath = filepath.Join(repoRoot, "runner-logs", "opencode", issueID+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}

	args := BuildArgs(repoRoot, prompt, model)
	env := BuildEnv(nil, configRoot, configDir)
	process, err := runner.Start(args, env, logPath)
	if err != nil {
		return err
	}
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:        logPath,
		OpenCodeLogDir: filepath.Join(os.Getenv("HOME"), ".local", "share", "opencode", "log"),
		TailLines:      50,
	})
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- watchdog.Monitor(process)
	}()

	select {
	case err := <-resultCh:
		return err
	case <-ctx.Done():
		_ = process.Kill()
		return ctx.Err()
	}
}

func RunWithACP(
	ctx context.Context,
	issueID string,
	repoRoot string,
	prompt string,
	model string,
	configRoot string,
	configDir string,
	logPath string,
	runner Runner,
	acpClient ACPClient,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if logPath == "" {
		logPath = filepath.Join(repoRoot, "runner-logs", "opencode", issueID+".jsonl")
	}
	if acpClient != nil {
		if err := acpClient.Run(ctx, issueID, logPath); err != nil {
			return err
		}
	}
	return RunWithContext(ctx, issueID, repoRoot, prompt, model, configRoot, configDir, logPath, runner)
}
