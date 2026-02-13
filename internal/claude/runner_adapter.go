package claude

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

const defaultBinary = "claude"

var structuredReviewVerdictLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_VERDICT\s*:\s*(pass|fail)(?:\s*DONE)?\s*$`)
var tokenRedactionPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`)

type CommandSpec struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

type CommandRunner interface {
	Run(ctx context.Context, spec CommandSpec) error
}

type commandRunnerFunc func(ctx context.Context, spec CommandSpec) error

func (f commandRunnerFunc) Run(ctx context.Context, spec CommandSpec) error {
	return f(ctx, spec)
}

type CLIRunnerAdapter struct {
	binary string
	runner CommandRunner
	now    func() time.Time
}

func NewCLIRunnerAdapter(binary string, runner CommandRunner) *CLIRunnerAdapter {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = defaultBinary
	}
	if runner == nil {
		runner = commandRunnerFunc(runCommand)
	}
	return &CLIRunnerAdapter{
		binary: resolvedBinary,
		runner: runner,
		now:    time.Now,
	}
}

func (a *CLIRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		return contracts.RunnerResult{}, errors.New("nil claude runner adapter")
	}
	if a.runner == nil {
		a.runner = commandRunnerFunc(runCommand)
	}
	if a.now == nil {
		a.now = time.Now
	}

	startedAt := a.now().UTC()
	logPath := resolveLogPath(request)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return contracts.RunnerResult{}, err
	}

	stdoutFile, err := os.Create(logPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer stdoutFile.Close()

	stderrPath := strings.TrimSuffix(logPath, ".jsonl") + ".stderr.log"
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer stderrFile.Close()

	emitProgress := func(source string, line string) {
		if request.OnProgress == nil {
			return
		}
		normalized := normalizeLine(line)
		if normalized == "" {
			return
		}
		if source == "stderr" {
			normalized = "stderr: " + normalized
		}
		request.OnProgress(contracts.RunnerProgress{
			Type:      "runner_output",
			Message:   normalized,
			Metadata:  map[string]string{"source": source},
			Timestamp: a.now().UTC(),
		})
	}

	stdoutWriter := newLineWriter(stdoutFile, func(line string) {
		emitProgress("stdout", line)
	})
	stderrWriter := newLineWriter(stderrFile, func(line string) {
		emitProgress("stderr", line)
	})

	runCtx := ctx
	cancel := func() {}
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()

	runErr := a.runner.Run(runCtx, CommandSpec{
		Binary: a.binary,
		Args:   buildArgs(request),
		Dir:    request.RepoRoot,
		Stdout: stdoutWriter,
		Stderr: stderrWriter,
	})
	stdoutWriter.Flush()
	stderrWriter.Flush()

	if runErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			runErr = context.DeadlineExceeded
		}
		if errors.Is(runCtx.Err(), context.Canceled) && errors.Is(runErr, context.Canceled) {
			runErr = context.Canceled
		}
	}

	finishedAt := a.now().UTC()
	result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)
	result.LogPath = logPath
	result.Artifacts = buildRunnerArtifacts(request, result)
	if result.Status == contracts.RunnerResultCompleted && request.Mode == contracts.RunnerModeReview {
		result.ReviewReady = hasStructuredPassVerdict(logPath)
	}
	return result, nil
}

func resolveLogPath(request contracts.RunnerRequest) string {
	if request.Metadata != nil {
		if path := strings.TrimSpace(request.Metadata["log_path"]); path != "" {
			return path
		}
	}
	if strings.TrimSpace(request.RepoRoot) != "" && strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join(request.RepoRoot, "runner-logs", "claude", request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", "claude", request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", "claude", "claude-run.jsonl")
}

func buildArgs(request contracts.RunnerRequest) []string {
	args := []string{"--print", "--output-format", "text"}
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	return args
}

func runCommand(ctx context.Context, spec CommandSpec) error {
	if strings.TrimSpace(spec.Binary) == "" {
		return errors.New("claude binary is required")
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	if strings.TrimSpace(spec.Dir) != "" {
		cmd.Dir = spec.Dir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	err := cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	return err
}

func buildRunnerArtifacts(request contracts.RunnerRequest, result contracts.RunnerResult) map[string]string {
	artifacts := map[string]string{
		"backend": "claude",
		"status":  string(result.Status),
	}
	if strings.TrimSpace(request.Model) != "" {
		artifacts["model"] = strings.TrimSpace(request.Model)
	}
	if strings.TrimSpace(string(request.Mode)) != "" {
		artifacts["mode"] = strings.TrimSpace(string(request.Mode))
	}
	if strings.TrimSpace(result.LogPath) != "" {
		artifacts["log_path"] = strings.TrimSpace(result.LogPath)
	}
	if request.Mode == contracts.RunnerModeReview {
		if verdict, ok := structuredReviewVerdict(result.LogPath); ok {
			artifacts["review_verdict"] = verdict
		}
	}
	if !result.StartedAt.IsZero() {
		artifacts["started_at"] = result.StartedAt.UTC().Format(time.RFC3339)
	}
	if !result.FinishedAt.IsZero() {
		artifacts["finished_at"] = result.FinishedAt.UTC().Format(time.RFC3339)
	}
	if request.Metadata != nil {
		if clonePath := strings.TrimSpace(request.Metadata["clone_path"]); clonePath != "" {
			artifacts["clone_path"] = clonePath
		}
	}
	if len(artifacts) == 0 {
		return nil
	}
	return artifacts
}

func hasStructuredPassVerdict(logPath string) bool {
	verdict, ok := structuredReviewVerdict(logPath)
	if !ok {
		return false
	}
	return strings.EqualFold(verdict, "pass")
}

func structuredReviewVerdict(logPath string) (string, bool) {
	if strings.TrimSpace(logPath) == "" {
		return "", false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return "", false
	}
	return lastStructuredVerdictLine(string(content))
}

func lastStructuredVerdictLine(text string) (string, bool) {
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	if normalized == "" {
		return "", false
	}
	lastVerdict := ""
	found := false
	for _, line := range strings.Split(normalized, "\n") {
		matches := structuredReviewVerdictLinePattern.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}
		lastVerdict = strings.ToLower(matches[1])
		found = true
	}
	return lastVerdict, found
}

func normalizeLine(line string) string {
	trimmed := strings.ReplaceAll(line, "\r", "")
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return ""
	}
	trimmed = tokenRedactionPattern.ReplaceAllString(trimmed, "<redacted-token>")
	const maxLen = 500
	if len(trimmed) > maxLen {
		trimmed = trimmed[:maxLen] + "..."
	}
	return trimmed
}

type lineWriter struct {
	target  io.Writer
	emit    func(string)
	mu      sync.Mutex
	pending strings.Builder
}

func newLineWriter(target io.Writer, emit func(string)) *lineWriter {
	return &lineWriter{target: target, emit: emit}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target != nil {
		if _, err := w.target.Write(p); err != nil {
			return 0, err
		}
	}
	if len(p) == 0 {
		return 0, nil
	}
	w.consumeLocked(string(p))
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending.Len() == 0 {
		return
	}
	if w.emit != nil {
		w.emit(w.pending.String())
	}
	w.pending.Reset()
}

func (w *lineWriter) consumeLocked(chunk string) {
	for _, r := range chunk {
		if r == '\n' {
			if w.emit != nil {
				w.emit(w.pending.String())
			}
			w.pending.Reset()
			continue
		}
		w.pending.WriteRune(r)
	}
}
