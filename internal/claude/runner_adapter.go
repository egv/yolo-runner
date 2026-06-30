package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	emitStructuredProgress := func(progress contracts.RunnerProgress) {
		if request.OnProgress == nil {
			return
		}
		if progress.Timestamp.IsZero() {
			progress.Timestamp = a.now().UTC()
		}
		request.OnProgress(progress)
	}
	streamProgress := newClaudeStreamProgressEmitter(emitStructuredProgress, a.now)

	stdoutWriter := newLineWriter(stdoutFile, func(line string) {
		emitProgress("stdout", line)
		streamProgress.HandleLine(line)
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

type claudeStreamProgressEmitter struct {
	emit         func(contracts.RunnerProgress)
	now          func() time.Time
	pendingTools map[string]claudePendingTool
}

type claudePendingTool struct {
	ID      string
	Name    string
	Command string
	Target  string
}

type claudeStreamEvent struct {
	Type      string                `json:"type"`
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Input     map[string]any        `json:"input"`
	ToolUseID string                `json:"tool_use_id"`
	IsError   bool                  `json:"is_error"`
	Text      string                `json:"text"`
	Content   []claudeNestedContent `json:"content"`
	Message   struct {
		Usage   map[string]json.RawMessage `json:"usage"`
		Content []claudeStreamContent      `json:"content"`
	} `json:"message"`
}

type claudeStreamContent struct {
	Type      string                `json:"type"`
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Input     map[string]any        `json:"input"`
	ToolUseID string                `json:"tool_use_id"`
	IsError   bool                  `json:"is_error"`
	Text      string                `json:"text"`
	Content   []claudeNestedContent `json:"content"`
}

type claudeNestedContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func newClaudeStreamProgressEmitter(emit func(contracts.RunnerProgress), now func() time.Time) *claudeStreamProgressEmitter {
	if now == nil {
		now = time.Now
	}
	return &claudeStreamProgressEmitter{
		emit:         emit,
		now:          now,
		pendingTools: map[string]claudePendingTool{},
	}
}

func (e *claudeStreamProgressEmitter) HandleLine(line string) {
	if e == nil || e.emit == nil || strings.TrimSpace(line) == "" || !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return
	}
	var event claudeStreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Type == "tool_use" {
		e.recordToolUse(claudeStreamContent{
			Type:  "tool_use",
			ID:    event.ID,
			Name:  event.Name,
			Input: event.Input,
		})
		return
	}
	if event.Type == "tool_result" {
		e.emitToolResult(claudeStreamContent{
			Type:      "tool_result",
			ToolUseID: event.ToolUseID,
			IsError:   event.IsError,
			Text:      event.Text,
			Content:   event.Content,
		})
		return
	}
	if event.Type != "assistant" {
		return
	}
	e.emitTokenUsage(event.Message.Usage)
	for _, content := range event.Message.Content {
		switch content.Type {
		case "tool_use":
			e.recordToolUse(content)
		case "tool_result":
			e.emitToolResult(content)
		}
	}
}

func (e *claudeStreamProgressEmitter) recordToolUse(content claudeStreamContent) {
	id := strings.TrimSpace(content.ID)
	name := strings.TrimSpace(content.Name)
	if id == "" || name == "" {
		return
	}
	e.pendingTools[id] = claudePendingTool{
		ID:      id,
		Name:    name,
		Command: claudeInputString(content.Input, "command"),
		Target:  claudeToolTarget(content.Input),
	}
}

func (e *claudeStreamProgressEmitter) emitToolResult(content claudeStreamContent) {
	id := strings.TrimSpace(content.ToolUseID)
	if id == "" {
		return
	}
	tool, ok := e.pendingTools[id]
	if !ok {
		return
	}
	delete(e.pendingTools, id)
	if strings.EqualFold(tool.Name, "bash") {
		e.emitCommandRun(tool, content)
		return
	}
	e.emitToolInvoked(tool, content)
}

func (e *claudeStreamProgressEmitter) emitCommandRun(tool claudePendingTool, result claudeStreamContent) {
	if strings.TrimSpace(tool.Command) == "" {
		return
	}
	metadata := map[string]string{
		"tool":    tool.Name,
		"command": tool.Command,
	}
	if exitCode, ok := claudeExtractExitCode(resultText(result)); ok {
		metadata["exit_code"] = strconv.Itoa(exitCode)
	}
	if durationMS, ok := claudeExtractDurationMS(resultText(result)); ok {
		metadata["duration_ms"] = strconv.FormatInt(durationMS, 10)
	}
	if result.IsError {
		metadata["outcome"] = "error"
	} else {
		metadata["outcome"] = "ok"
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeCommandRun),
		Message:   tool.Command,
		Metadata:  metadata,
		Timestamp: e.now().UTC(),
	})
}

func (e *claudeStreamProgressEmitter) emitToolInvoked(tool claudePendingTool, result claudeStreamContent) {
	metadata := map[string]string{
		"tool":    tool.Name,
		"outcome": claudeToolOutcome(result),
	}
	if strings.TrimSpace(tool.Target) != "" {
		metadata["target"] = tool.Target
		metadata["path"] = tool.Target
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeToolInvoked),
		Message:   strings.TrimSpace(tool.Name),
		Metadata:  metadata,
		Timestamp: e.now().UTC(),
	})
}

func (e *claudeStreamProgressEmitter) emitTokenUsage(usage map[string]json.RawMessage) {
	if len(usage) == 0 {
		return
	}
	metadata := map[string]string{}
	for key, raw := range usage {
		value, ok := claudeJSONNumberString(raw)
		if !ok {
			continue
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		return
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeTokenUsage),
		Metadata:  metadata,
		Timestamp: e.now().UTC(),
	})
}

func claudeInputString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := input[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		}
	}
	return ""
}

func claudeToolTarget(input map[string]any) string {
	return claudeInputString(input, "file_path", "path", "notebook_path", "url", "pattern")
}

func resultText(content claudeStreamContent) string {
	var parts []string
	if content.Text != "" {
		parts = append(parts, content.Text)
	}
	for _, nested := range content.Content {
		if nested.Text != "" {
			parts = append(parts, nested.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func claudeToolOutcome(result claudeStreamContent) string {
	if !result.IsError {
		return "ok"
	}
	if stdinLooksLikePermissionDenied(resultText(result)) {
		return "denied"
	}
	return "error"
}

var claudeExitCodePattern = regexp.MustCompile(`(?im)\b(?:exit[_ ]code|Exit code)\s*:?\s*(-?\d+)\b`)
var claudeDurationPattern = regexp.MustCompile(`(?im)\b(?:duration[_ ]ms|duration)\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*(ms|millisecond(?:s)?|s|sec|second(?:s)?)?\b`)

func claudeExtractExitCode(text string) (int, bool) {
	matches := claudeExitCodePattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, false
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

func claudeExtractDurationMS(text string) (int64, bool) {
	matches := claudeDurationPattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, false
	}
	unit := "ms"
	if len(matches) > 2 && strings.TrimSpace(matches[2]) != "" {
		unit = strings.ToLower(matches[2])
	}
	if strings.HasPrefix(unit, "s") {
		value *= 1000
	}
	return int64(value), true
}

func claudeJSONNumberString(raw json.RawMessage) (string, bool) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String(), true
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err == nil {
		return strconv.FormatFloat(value, 'f', -1, 64), true
	}
	return "", false
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
		for _, candidate := range expandJSONLLine(line) {
			matches := structuredReviewVerdictLinePattern.FindStringSubmatch(candidate)
			if len(matches) < 2 {
				continue
			}
			lastVerdict = strings.ToLower(matches[1])
			found = true
		}
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
		for _, candidate := range expandJSONLLine(line) {
			matches := structuredReviewFailFeedbackLinePattern.FindStringSubmatch(candidate)
			if len(matches) < 2 {
				continue
			}
			feedback := strings.Join(strings.Fields(matches[1]), " ")
			if feedback == "" {
				continue
			}
			lastFeedback = feedback
			found = true
		}
	}
	return lastFeedback, found
}

// expandJSONLLine returns the candidate text lines to match against for a single
// log file line. For plain-text logs it returns the line itself. For stream-json
// JSONL logs it additionally returns the extracted text content of assistant
// messages so that REVIEW_VERDICT / REVIEW_FAIL_FEEDBACK markers are visible.
func expandJSONLLine(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return []string{line}
	}
	var msg struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.Type != "assistant" {
		return []string{line}
	}
	var texts []string
	for _, c := range msg.Message.Content {
		if c.Type == "text" {
			texts = append(texts, strings.Split(c.Text, "\n")...)
		}
	}
	if len(texts) == 0 {
		return []string{line}
	}
	return texts
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
