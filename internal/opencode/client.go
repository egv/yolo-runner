package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/anomalyco/yolo-runner/internal/logging"
	acp "github.com/ironpark/acp-go"
	"golang.org/x/term"
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
	acpShutdownGrace = 2 * time.Second
)

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

func BuildEnv(baseEnv map[string]string, configRoot string, configDir string, model string) map[string]string {
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
		configContent := map[string]string{}
		if model != "" {
			configContent["model"] = model
		}
		payload, err := json.Marshal(configContent)
		if err != nil {
			payload = []byte("{}")
		}
		env["OPENCODE_CONFIG_CONTENT"] = string(payload)
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

	args := BuildACPArgsWithModel(repoRoot, model)
	env := BuildEnv(nil, configRoot, configDir, model)
	process, err := runner.Start(args, env, logPath)
	if err != nil {
		return err
	}

	if acpClient == nil {
		type stdioProcess interface {
			Stdin() io.WriteCloser
			Stdout() io.ReadCloser
		}
		stdio, ok := process.(stdioProcess)
		if !ok {
			return errors.New("opencode runner does not expose stdin/stdout for ACP")
		}
		acpClient = ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
			handler := NewACPHandler(issueID, logPath, func(logPath string, issueID string, requestType string, decision string) error {
				if line := formatACPRequest(requestType, decision); line != "" {
					writeConsoleLine(os.Stderr, fmt.Sprintf("ACP[%s] %s", issueID, line))
				}
				return logging.AppendACPRequest(logPath, logging.ACPRequestEntry{
					IssueID:     issueID,
					RequestType: requestType,
					Decision:    decision,
				})
			})
			aggregator := NewAgentMessageAggregator()
			onUpdate := func(note *acp.SessionNotification) {
				if note == nil {
					return
				}
				if line := aggregator.ProcessUpdate(&note.Update); line != "" {
					writeConsoleLine(os.Stderr, fmt.Sprintf("ACP[%s] %s", issueID, line))
				}
			}
			return RunACPClient(ctx, stdio.Stdin(), stdio.Stdout(), repoRoot, prompt, handler, onUpdate)
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

func writeConsoleLine(out io.Writer, line string) {
	if out == nil || line == "" {
		return
	}
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		fmt.Fprintf(out, "\r\x1b[2K%s\r\n", line)
		return
	}
	fmt.Fprintln(out, line)
}
