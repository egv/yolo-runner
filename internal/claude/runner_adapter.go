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

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const defaultBinary = "claude"

var structuredReviewVerdictLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_VERDICT\s*:\s*(pass|fail)(?:\s*DONE)?\s*$`)
var structuredReviewFailFeedbackLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_(?:FAIL_)?FEEDBACK\s*:\s*(.+?)\s*$`)

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
	args   []string
	runner CommandRunner
	now    func() time.Time
}

func NewCLIRunnerAdapter(binary string, runner CommandRunner, args ...string) *CLIRunnerAdapter {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = defaultBinary
	}
	if runner == nil {
		runner = commandRunnerFunc(runCommand)
	}
	normalizedArgs := append([]string(nil), args...)
	return &CLIRunnerAdapter{
		binary: resolvedBinary,
		args:   normalizedArgs,
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
	defer func() { _ = stdoutFile.Close() }()

	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer func() { _ = stderrFile.Close() }()

	emitProgress := func(source string, line string) {
		if request.OnProgress == nil {
			return
		}
		progress, ok := contracts.NewRunnerOutputProgress(source, line, a.now().UTC())
		if !ok {
			return
		}
		request.OnProgress(progress)
	}

	stdoutWriter := newLineWriter(stdoutFile, func(line string) {
		emitProgress("stdout", line)
	})
	stderrWriter := newLineWriter(stderrFile, func(line string) {
		emitProgress("stderr", line)
	})

	runCtx, cancel := contracts.WithOptionalTimeout(ctx, request.Timeout)
	defer cancel()

	runErr := a.runner.Run(runCtx, CommandSpec{
		Binary: a.binary,
		Args:   a.buildArgs(request),
		Dir:    request.RepoRoot,
		Stdout: stdoutWriter,
		Stderr: stderrWriter,
	})
	stdoutWriter.Flush()
	stderrWriter.Flush()

	runErr = contracts.FinalizeRunError(runCtx, runErr)

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

func (a *CLIRunnerAdapter) buildArgs(request contracts.RunnerRequest) []string {
	if len(a.args) > 0 {
		return resolveBackendArgs(a.args, "claude", request)
	}
	return defaultBuildArgs(request)
}

func resolveBackendArgs(raw []string, backend string, request contracts.RunnerRequest) []string {
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = "claude"
	}
	requestBackend := strings.TrimSpace(request.Metadata["backend"])
	if requestBackend != "" {
		backend = requestBackend
	}

	out := make([]string, 0, len(raw))
	template := map[string]string{
		"{{backend}}":      backend,
		"{{backend-name}}": backend,
		"{{model}}":        strings.TrimSpace(request.Model),
		"{{prompt}}":       strings.TrimSpace(request.Prompt),
		"{{task_id}}":      strings.TrimSpace(request.TaskID),
		"{{repo_root}}":    strings.TrimSpace(request.RepoRoot),
		"{{mode}}":         strings.TrimSpace(string(request.Mode)),
	}

	for _, value := range raw {
		text := strings.TrimSpace(value)
		for placeholder, replacement := range template {
			text = strings.ReplaceAll(text, placeholder, replacement)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func defaultBuildArgs(request contracts.RunnerRequest) []string {
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
	extras := map[string]string{}
	if request.Mode == contracts.RunnerModeReview {
		if verdict, ok := structuredReviewVerdict(result.LogPath); ok {
			extras["review_verdict"] = verdict
			if verdict == "fail" {
				if feedback, ok := structuredReviewFailFeedback(result.LogPath); ok {
					extras["review_fail_feedback"] = feedback
				}
			}
		}
	}
	return contracts.BuildRunnerArtifacts("claude", request, result, extras)
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

func structuredReviewFailFeedback(logPath string) (string, bool) {
	if strings.TrimSpace(logPath) == "" {
		return "", false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return "", false
	}
	return lastStructuredReviewFailFeedbackLine(string(content))
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

func lastStructuredReviewFailFeedbackLine(text string) (string, bool) {
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	if normalized == "" {
		return "", false
	}
	lastFeedback := ""
	found := false
	for _, line := range strings.Split(normalized, "\n") {
		matches := structuredReviewFailFeedbackLinePattern.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}
		candidate := strings.Join(strings.Fields(matches[1]), " ")
		if candidate == "" {
			continue
		}
		lastFeedback = candidate
		found = true
	}
	return lastFeedback, found
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
