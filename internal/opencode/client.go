package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type watchdogRuntimeConfig struct {
	Timeout        time.Duration
	Interval       time.Duration
	OpenCodeLogDir string
}

type watchdogRuntimeConfigContextKey struct{}

func withWatchdogRuntimeConfig(ctx context.Context, config watchdogRuntimeConfig) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, watchdogRuntimeConfigContextKey{}, config)
}

func watchdogRuntimeConfigFromContext(ctx context.Context) watchdogRuntimeConfig {
	if ctx == nil {
		return watchdogRuntimeConfig{}
	}
	value := ctx.Value(watchdogRuntimeConfigContextKey{})
	if value == nil {
		return watchdogRuntimeConfig{}
	}
	config, ok := value.(watchdogRuntimeConfig)
	if !ok {
		return watchdogRuntimeConfig{}
	}
	return config
}

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
	// Ensure OpenCode never blocks on permission prompts.
	permission := map[string]string{
		"*":                  "allow",
		"doom_loop":          "allow",
		"external_directory": "allow",
		"question":           "allow",
		"plan_enter":         "allow",
		"plan_exit":          "allow",
	}
	if payload, err := json.Marshal(permission); err == nil {
		env["OPENCODE_PERMISSION"] = string(payload)
	}

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
	return RunWithACPAndUpdates(ctx, issueID, repoRoot, prompt, model, configRoot, configDir, logPath, runner, acpClient, nil)
}

func RunWithACPAndUpdates(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpClient ACPClient, onLineUpdate func(string)) error {
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
			handler := NewACPHandler(issueID, logPath, func(logPath string, issueID string, requestType string, decision string, detail string) error {
				if line := forwardACPRequestLine(requestType, decision, detail, onLineUpdate); line != "" {
					writeConsoleLine(os.Stderr, fmt.Sprintf("ACP[%s] %s", issueID, line))
				}
				return logging.AppendACPRequest(logPath, logging.ACPRequestEntry{
					IssueID:     issueID,
					RequestType: requestType,
					Decision:    decision,
					Message:     normalizeACPRequestDetail(detail),
				})
			})
			aggregator := NewAgentMessageAggregator()
			onUpdate := func(note *acp.SessionNotification) {
				if note == nil {
					return
				}
				if line := aggregator.ProcessUpdate(&note.Update); line != "" {
					if onLineUpdate != nil {
						onLineUpdate(line)
					}
					// Skip writing tool calls directly to console - they should go to log bubble instead
					// Only write non-tool-call messages to console
					if !strings.HasPrefix(line, "⏳") && !strings.HasPrefix(line, "🔄") && !strings.HasPrefix(line, "✅") && !strings.HasPrefix(line, "❌") && !strings.HasPrefix(line, "⚪") {
						writeConsoleLine(os.Stderr, fmt.Sprintf("ACP[%s] %s", issueID, line))
					}
					_ = logging.AppendACPRequest(logPath, logging.ACPRequestEntry{
						IssueID:     issueID,
						RequestType: "update",
						Decision:    "allow",
						Message:     line,
					})
				}
			}
			return RunACPClient(ctx, stdio.Stdin(), stdio.Stdout(), repoRoot, prompt, handler, onUpdate)
		})
	}
	process = newWaitOnceProcess(process)
	watchdogConfig := WatchdogConfig{LogPath: logPath}
	watchdogRuntime := watchdogRuntimeConfigFromContext(ctx)
	if watchdogRuntime.Timeout > 0 {
		watchdogConfig.Timeout = watchdogRuntime.Timeout
	}
	if watchdogRuntime.Interval > 0 {
		watchdogConfig.Interval = watchdogRuntime.Interval
	}
	if strings.TrimSpace(watchdogRuntime.OpenCodeLogDir) != "" {
		watchdogConfig.OpenCodeLogDir = watchdogRuntime.OpenCodeLogDir
	}
	watchdog := NewWatchdog(watchdogConfig)

	// Add timeout mechanism for detecting stuck OpenCode processes and initialization failures
	initTimeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < initTimeout {
			initTimeout = remaining
		}
	}
	serenaSince := time.Time{}

	// Monitor for initialization failures
	initCtx, initCancel := context.WithTimeout(ctx, initTimeout)
	defer initCancel()

	// Check stderr logs for Serena initialization failures
	stderrPath := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	serenaErrCh := make(chan error, 1)
	if line, ok := findSerenaInitErrorSince(stderrPath, serenaSince); ok {
		serenaErr := fmt.Errorf("serena initialization failed: %s", line)
		writeConsoleLine(os.Stderr, serenaErr.Error())
		serenaErrCh <- serenaErr
	} else {
		go monitorInitFailures(initCtx, stderrPath, serenaErrCh, serenaSince)
	}

	// Run ACP client in goroutine to avoid blocking
	acpErrCh := make(chan error, 1)
	go func() {
		acpErrCh <- acpClient.Run(ctx, issueID, logPath)
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- watchdog.Monitor(process)
	}()

	var killErr error
	var waitErr error
	var runErr error
	forcedKillAfterACP := false

	acpCh := acpErrCh
	wCh := waitCh

	for acpCh != nil || wCh != nil {
		select {
		case serenaErr := <-serenaErrCh:
			killErr = process.Kill()
			if wCh != nil {
				waitErr = <-wCh
				wCh = nil
			}
			return errors.Join(serenaErr, killErr, waitErr)
		case <-ctx.Done():
			killErr = process.Kill()
			if wCh != nil {
				waitErr = <-wCh
				wCh = nil
			}
			return errors.Join(ctx.Err(), killErr, waitErr)
		case runErr = <-acpCh:
			// ACP finished. Do not wait indefinitely for the opencode process to exit.
			acpCh = nil
			if wCh == nil {
				continue
			}
			t := time.NewTimer(acpShutdownGrace)
			select {
			case waitErr = <-wCh:
				wCh = nil
				if !t.Stop() {
					<-t.C
				}
			case <-t.C:
				forcedKillAfterACP = true
				killErr = process.Kill()
				waitErr = <-wCh
				wCh = nil
			}
		case waitErr = <-wCh:
			wCh = nil
			if acpCh == nil {
				continue
			}
			// Process exited before ACP finished; give ACP a short grace window to drain.
			t := time.NewTimer(acpShutdownGrace)
			select {
			case runErr = <-acpCh:
				acpCh = nil
				if !t.Stop() {
					<-t.C
				}
			case <-t.C:
				acpCh = nil
				runErr = fmt.Errorf("acp client did not finish after opencode exit")
			}
		}
	}

	if line, ok := findSerenaInitErrorSince(stderrPath, serenaSince); ok {
		serenaErr := fmt.Errorf("serena initialization failed: %s", line)
		writeConsoleLine(os.Stderr, serenaErr.Error())
		return errors.Join(serenaErr, killErr, waitErr, runErr)
	}

	shutdownErr := errors.Join(killErr, waitErr)
	if runErr != nil {
		return errors.Join(runErr, shutdownErr)
	}
	if forcedKillAfterACP && shutdownErr != nil {
		// ACP completed successfully, but opencode didn't exit on its own.
		// Treat as success to avoid hanging the runner.
		writeConsoleLine(os.Stderr, fmt.Sprintf("opencode did not exit cleanly after ACP completion: %v", shutdownErr))
		return nil
	}
	return shutdownErr
}

func forwardACPRequestLine(requestType string, decision string, detail string, onLineUpdate func(string)) string {
	line := formatACPRequestDetail(requestType, decision, detail)
	if line != "" && onLineUpdate != nil {
		onLineUpdate(line)
	}
	return line
}

type waitOnceProcess struct {
	Process
	once     sync.Once
	waitErr  error
	waitDone chan struct{}
}

func newWaitOnceProcess(process Process) *waitOnceProcess {
	return &waitOnceProcess{Process: process, waitDone: make(chan struct{})}
}

func (p *waitOnceProcess) Wait() error {
	p.once.Do(func() {
		p.waitErr = p.Process.Wait()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
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
