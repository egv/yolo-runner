package codingagents

import (
	"context"
	"errors"
	"io"
	"regexp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// CommandSpec matches the command invocation contract used by built-in adapters.
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

func (fn commandRunnerFunc) Run(ctx context.Context, spec CommandSpec) error {
	return fn(ctx, spec)
}

// GenericCLIRunnerAdapter executes command-based coding agents.
type GenericCLIRunnerAdapter struct {
	backend string
	binary  string
	args    []string
	runner  CommandRunner
	now     func() time.Time
}

var structuredReviewVerdictLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_VERDICT\s*:\s*(pass|fail)(?:\s*DONE)?\s*$`)

func NewGenericCLIRunnerAdapter(backend string, binary string, args []string, runner CommandRunner) *GenericCLIRunnerAdapter {
	if strings.TrimSpace(backend) == "" {
		backend = "coding-agent"
	}
	if runner == nil {
		runner = commandRunnerFunc(runCommand)
	}
	return &GenericCLIRunnerAdapter{
		backend: strings.ToLower(strings.TrimSpace(backend)),
		binary:  strings.TrimSpace(binary),
		args:    append([]string(nil), normalizeStringSlice(args)...),
		runner:  runner,
		now:     time.Now,
	}
}

func (a *GenericCLIRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		return contracts.RunnerResult{}, errors.New("nil command runner adapter")
	}
	if strings.TrimSpace(a.binary) == "" {
		return contracts.RunnerResult{}, errors.New("binary is required")
	}
	if a.runner == nil {
		a.runner = commandRunnerFunc(runCommand)
	}
	if a.now == nil {
		a.now = time.Now
	}
	request = requestWithBackend(request, a.backend)

	startedAt := a.now().UTC()
	logPath := resolveLogPath(request, a.backend)
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

	commandArgs := resolveCommandArgs(a.args, request)

	runCtx := ctx
	cancel := func() {}
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()

	runErr := a.runner.Run(runCtx, CommandSpec{
		Binary: a.binary,
		Args:   commandArgs,
		Dir:    request.RepoRoot,
		Stdout: stdoutWriter,
		Stderr: stderrWriter,
	})
	stdoutWriter.Flush()
	stderrWriter.Flush()

	if runErr == nil && runCtx.Err() != nil {
		runErr = runCtx.Err()
	}
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
	result.Artifacts = map[string]string{
		"backend": a.backend,
		"status":  string(result.Status),
	}
	if request.Mode == contracts.RunnerModeReview {
		if verdict, ok := structuredReviewVerdict(logPath); ok {
			result.Artifacts["review_verdict"] = verdict
			result.ReviewReady = strings.EqualFold(verdict, "pass")
		}
	}
	if strings.TrimSpace(request.Model) != "" {
		result.Artifacts["model"] = strings.TrimSpace(request.Model)
	}
	if strings.TrimSpace(string(request.Mode)) != "" {
		result.Artifacts["mode"] = strings.TrimSpace(string(request.Mode))
	}
	if strings.TrimSpace(logPath) != "" {
		result.Artifacts["log_path"] = strings.TrimSpace(logPath)
	}
	if !result.StartedAt.IsZero() {
		result.Artifacts["started_at"] = result.StartedAt.UTC().Format(time.RFC3339)
	}
	if !result.FinishedAt.IsZero() {
		result.Artifacts["finished_at"] = result.FinishedAt.UTC().Format(time.RFC3339)
	}
	if request.Metadata != nil {
		if clonePath := strings.TrimSpace(request.Metadata["clone_path"]); clonePath != "" {
			result.Artifacts["clone_path"] = clonePath
		}
	}
	if len(result.Artifacts) == 0 {
		return result, nil
	}
	return result, nil
}

func structuredReviewVerdict(logPath string) (string, bool) {
	if strings.TrimSpace(logPath) == "" {
		return "", false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return "", false
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(content))
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
	if !found {
		return "", false
	}
	return lastVerdict, true
}

func resolveLogPath(request contracts.RunnerRequest, backend string) string {
	if request.Metadata != nil {
		if path := strings.TrimSpace(request.Metadata["log_path"]); path != "" {
			return path
		}
	}
	backend = normalizeBackend(backend)
	if backend == "" {
		backend = "coding-agent"
	}
	if strings.TrimSpace(request.RepoRoot) != "" && strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join(request.RepoRoot, "runner-logs", backend, request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", backend, request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", backend, backend+"-run.jsonl")
}

func requestWithBackend(request contracts.RunnerRequest, backend string) contracts.RunnerRequest {
	if strings.TrimSpace(backend) == "" {
		return request
	}
	metadata := map[string]string{}
	for key, value := range request.Metadata {
		metadata[key] = value
	}
	if strings.TrimSpace(metadata["backend"]) == "" {
		metadata["backend"] = backend
	}
	request.Metadata = metadata
	return request
}

func runCommand(ctx context.Context, spec CommandSpec) error {
	if strings.TrimSpace(spec.Binary) == "" {
		return errors.New("binary is required")
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

func resolveCommandArgs(raw []string, request contracts.RunnerRequest) []string {
	out := make([]string, 0, len(raw))
	backend := strings.ToLower(strings.TrimSpace(request.Metadata["backend"]))
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

func normalizeLine(raw string) string {
	return strings.TrimSpace(strings.ReplaceAll(raw, "\x00", ""))
}

// newLineWriter replicates existing line-oriented stdout/stderr writers used by built-in adapters.
type lineWriter struct {
	buffer string
	write  func(string)
	target io.Writer
}

func newLineWriter(target io.Writer, write func(string)) *lineWriter {
	return &lineWriter{target: target, write: write}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			line := strings.TrimSuffix(w.buffer, "\n")
			if _, err := w.target.Write([]byte(line + "\n")); err != nil {
				return 0, err
			}
			w.write(line)
			w.buffer = ""
			continue
		}
		w.buffer += string(b)
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	if strings.TrimSpace(w.buffer) == "" {
		return
	}
	line := strings.TrimSuffix(w.buffer, "\n")
	_, _ = w.target.Write([]byte(line + "\n"))
	w.write(line)
	w.buffer = ""
}
