package opencode

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/anomalyco/yolo-runner/internal/logging"
	acp "github.com/ironpark/acp-go"
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

const (
	acpReadyRetryDelay = 10 * time.Millisecond
	acpShutdownGrace   = 2 * time.Second
)

var acpReadyTimeout = 2 * time.Second

func BuildArgs(repoRoot string, prompt string, model string) []string {
	args := []string{"opencode", "run", prompt, "--agent", "yolo", "--format", "json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, repoRoot)
	return args
}

func RedactArgs(args []string) []string {
	if len(args) >= 3 && args[0] == "opencode" && args[1] == "run" {
		redacted := append([]string{}, args...)
		redacted[2] = "<prompt redacted>"
		return redacted
	}
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
	return RunWithACP(context.Background(), issueID, repoRoot, prompt, model, configRoot, configDir, logPath, runner, nil)
}

func RunWithContext(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner) error {
	return RunWithACP(ctx, issueID, repoRoot, prompt, model, configRoot, configDir, logPath, runner, nil)
}

func RunWithACP(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpClient ACPClient) error {
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return err
	}

	endpoint := fmt.Sprintf("127.0.0.1:%d", port)
	args := BuildACPArgs(repoRoot, port)
	env := BuildEnv(nil, configRoot, configDir)
	process, err := runner.Start(args, env, logPath)
	if err != nil {
		return err
	}

	if acpClient == nil {
		acpClient = ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
			readyCtx, cancel := context.WithTimeout(ctx, acpReadyTimeout)
			defer cancel()
			if err := waitForACPReady(readyCtx, endpoint, acpReadyRetryDelay); err != nil {
				return err
			}
			handler := NewACPHandler(issueID, logPath, func(logPath string, issueID string, requestType string, decision string) error {
				if line := formatACPRequest(requestType, decision); line != "" {
					fmt.Fprintf(os.Stderr, "ACP[%s] %s\n", issueID, line)
				}
				return logging.AppendACPRequest(logPath, logging.ACPRequestEntry{
					IssueID:     issueID,
					RequestType: requestType,
					Decision:    decision,
				})
			})
			onUpdate := func(note *acp.SessionNotification) {
				if note == nil {
					return
				}
				if line := formatSessionUpdate(&note.Update); line != "" {
					fmt.Fprintf(os.Stderr, "ACP[%s] %s\n", issueID, line)
				}
			}
			return RunACPClient(ctx, endpoint, repoRoot, prompt, handler, onUpdate)
		})
	}

	runErr := acpClient.Run(ctx, issueID, logPath)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- process.Wait()
	}()

	var killErr error
	var waitErr error
	timeout := time.NewTimer(acpShutdownGrace)
	defer timeout.Stop()
	select {
	case waitErr = <-waitCh:
		if !timeout.Stop() {
			<-timeout.C
		}
	case <-ctx.Done():
		killErr = process.Kill()
		waitErr = <-waitCh
	case <-timeout.C:
		killErr = process.Kill()
		waitErr = <-waitCh
	}
	shutdownErr := errors.Join(killErr, waitErr)
	if runErr != nil {
		return errors.Join(runErr, shutdownErr)
	}
	return shutdownErr
}

func waitForACPReady(ctx context.Context, endpoint string, retryDelay time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if retryDelay <= 0 {
		retryDelay = acpReadyRetryDelay
	}
	for {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "tcp", endpoint)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}
