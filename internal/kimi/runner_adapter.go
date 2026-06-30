package kimi

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

const defaultBinary = "kimi"

var structuredReviewVerdictLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_VERDICT\s*:\s*(pass|fail)(?:\s*DONE)?\s*$`)
var structuredReviewFailFeedbackLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_(?:FAIL_)?FEEDBACK\s*:\s*(.+?)\s*$`)
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
		return contracts.RunnerResult{}, errors.New("nil kimi runner adapter")
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

	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer stderrFile.Close()

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
	streamProgress := newKimiStreamProgressEmitter(emitStructuredProgress, a.now)

	stdoutWriter := newLineWriter(stdoutFile, func(line string) {
		if !streamProgress.HandleLine(line) {
			emitProgress("stdout", line)
		}
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
		return filepath.Join(request.RepoRoot, "runner-logs", "kimi", request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", "kimi", request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", "kimi", "kimi-run.jsonl")
}

func (a *CLIRunnerAdapter) buildArgs(request contracts.RunnerRequest) []string {
	if len(a.args) > 0 {
		return resolveBackendArgs(a.args, "kimi", request)
	}
	return defaultBuildArgs(request)
}

func defaultBuildArgs(request contracts.RunnerRequest) []string {
	args := []string{"--print", "--output-format", "text", "--yolo"}
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	return args
}

func resolveBackendArgs(raw []string, backend string, request contracts.RunnerRequest) []string {
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = "kimi"
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

func runCommand(ctx context.Context, spec CommandSpec) error {
	if strings.TrimSpace(spec.Binary) == "" {
		return errors.New("kimi binary is required")
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
	return contracts.BuildRunnerArtifacts("kimi", request, result, extras)
}

type kimiStreamProgressEmitter struct {
	emit         func(contracts.RunnerProgress)
	now          func() time.Time
	pendingTools map[string]kimiPendingTool
}

type kimiPendingTool struct {
	ID      string
	Name    string
	Command string
	Target  string
}

type kimiStreamEvent struct {
	Type      string                     `json:"type"`
	Subtype   string                     `json:"subtype"`
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Input     map[string]any             `json:"input"`
	ToolUseID string                     `json:"tool_use_id"`
	IsError   bool                       `json:"is_error"`
	Text      string                     `json:"text"`
	Narration string                     `json:"narration"`
	Message   json.RawMessage            `json:"message"`
	Content   []kimiNestedContent        `json:"content"`
	Usage     map[string]json.RawMessage `json:"usage"`
}

type kimiStreamMessage struct {
	Text      string                     `json:"text"`
	Narration string                     `json:"narration"`
	Content   []kimiStreamContent        `json:"content"`
	Usage     map[string]json.RawMessage `json:"usage"`
}

type kimiStreamContent struct {
	Type      string              `json:"type"`
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Input     map[string]any      `json:"input"`
	ToolUseID string              `json:"tool_use_id"`
	IsError   bool                `json:"is_error"`
	Text      string              `json:"text"`
	Narration string              `json:"narration"`
	Content   []kimiNestedContent `json:"content"`
}

type kimiNestedContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func newKimiStreamProgressEmitter(emit func(contracts.RunnerProgress), now func() time.Time) *kimiStreamProgressEmitter {
	if now == nil {
		now = time.Now
	}
	return &kimiStreamProgressEmitter{
		emit:         emit,
		now:          now,
		pendingTools: map[string]kimiPendingTool{},
	}
}

func (e *kimiStreamProgressEmitter) HandleLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if e == nil || e.emit == nil || trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var event kimiStreamEvent
	if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
		return false
	}
	message, messageText := kimiDecodeStreamMessage(event.Message)

	handled := false
	if e.emitTokenUsage(event.Usage) {
		handled = true
	}
	if e.emitTokenUsage(message.Usage) {
		handled = true
	}
	if e.emitBlockedEvent(event.Type, event.Subtype, messageText, message.Text, event.Text, event.Narration) {
		handled = true
	}
	if !kimiIsBlockedEventType(event.Type, event.Subtype) {
		if e.emitNarration(event.Text) || e.emitNarration(event.Narration) || e.emitNarration(message.Text) || e.emitNarration(message.Narration) {
			handled = true
		}
	}

	if event.Type == "tool_use" {
		e.recordToolUse(kimiStreamContent{Type: "tool_use", ID: event.ID, Name: event.Name, Input: event.Input})
		return true
	}
	if event.Type == "tool_result" {
		e.emitToolResult(kimiStreamContent{
			Type:      "tool_result",
			ToolUseID: event.ToolUseID,
			IsError:   event.IsError,
			Text:      event.Text,
			Content:   event.Content,
		})
		return true
	}

	for _, content := range message.Content {
		if e.handleContent(content) {
			handled = true
		}
	}
	for _, content := range event.Content {
		if text := strings.TrimSpace(content.Text); text != "" {
			if e.emitNarration(text) {
				handled = true
			}
		}
	}
	return handled || event.Type != ""
}

func kimiDecodeStreamMessage(raw json.RawMessage) (kimiStreamMessage, string) {
	if len(raw) == 0 {
		return kimiStreamMessage{}, ""
	}
	var message kimiStreamMessage
	if err := json.Unmarshal(raw, &message); err == nil {
		return message, strings.TrimSpace(message.Text)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return kimiStreamMessage{Text: text}, strings.TrimSpace(text)
	}
	return kimiStreamMessage{}, ""
}

func (e *kimiStreamProgressEmitter) handleContent(content kimiStreamContent) bool {
	switch content.Type {
	case "text", "narration", "":
		return e.emitNarration(content.Text) || e.emitNarration(content.Narration)
	case "tool_use":
		e.recordToolUse(content)
		return true
	case "tool_result":
		e.emitToolResult(content)
		return true
	case "error", "denial":
		return e.emitBlocked(strings.TrimSpace(content.Text), content.Type)
	default:
		return false
	}
}

func (e *kimiStreamProgressEmitter) recordToolUse(content kimiStreamContent) {
	id := strings.TrimSpace(content.ID)
	name := strings.TrimSpace(content.Name)
	if id == "" || name == "" {
		return
	}
	e.pendingTools[id] = kimiPendingTool{
		ID:      id,
		Name:    name,
		Command: kimiInputString(content.Input, "command", "cmd"),
		Target:  kimiToolTarget(content.Input),
	}
}

func (e *kimiStreamProgressEmitter) emitToolResult(content kimiStreamContent) {
	id := strings.TrimSpace(content.ToolUseID)
	if id == "" {
		if content.IsError {
			e.emitBlocked(resultText(content), "error")
		}
		return
	}
	tool, ok := e.pendingTools[id]
	if !ok {
		if content.IsError {
			e.emitBlocked(resultText(content), "error")
		}
		return
	}
	delete(e.pendingTools, id)
	if content.IsError {
		e.emitBlocked(resultText(content), "error")
	}
	if strings.EqualFold(tool.Name, "bash") || strings.EqualFold(tool.Name, "shell") || strings.EqualFold(tool.Name, "command") {
		e.emitCommandRun(tool, content)
	}
}

func (e *kimiStreamProgressEmitter) emitCommandRun(tool kimiPendingTool, result kimiStreamContent) {
	if strings.TrimSpace(tool.Command) == "" {
		return
	}
	metadata := map[string]string{
		"tool":    tool.Name,
		"command": tool.Command,
	}
	if result.IsError {
		metadata["outcome"] = "error"
	} else {
		metadata["outcome"] = "ok"
	}
	text := resultText(result)
	if exitCode, ok := kimiExtractExitCode(text); ok {
		metadata["exit_code"] = strconv.Itoa(exitCode)
	}
	if durationMS, ok := kimiExtractDurationMS(text); ok {
		metadata["duration_ms"] = strconv.FormatInt(durationMS, 10)
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeCommandRun),
		Message:   tool.Command,
		Metadata:  metadata,
		Timestamp: e.now().UTC(),
	})
}

func (e *kimiStreamProgressEmitter) emitNarration(text string) bool {
	message := normalizeLine(text)
	if message == "" {
		return false
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeAgentText),
		Message:   message,
		Metadata:  map[string]string{"source": "stdout"},
		Timestamp: e.now().UTC(),
	})
	return true
}

func (e *kimiStreamProgressEmitter) emitBlockedEvent(eventType string, subtype string, candidates ...string) bool {
	if !kimiIsBlockedEventType(eventType, subtype) {
		return false
	}
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	for _, candidate := range candidates {
		if e.emitBlocked(candidate, eventType) {
			return true
		}
	}
	return e.emitBlocked(eventType, eventType)
}

func kimiIsBlockedEventType(eventType string, subtype string) bool {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	subtype = strings.ToLower(strings.TrimSpace(subtype))
	return eventType == "error" || eventType == "denial" || eventType == "denied" || subtype == "error" || subtype == "denial" || subtype == "denied"
}

func (e *kimiStreamProgressEmitter) emitBlocked(message string, reason string) bool {
	message = normalizeLine(message)
	if message == "" {
		return false
	}
	metadata := map[string]string{}
	if reason = strings.TrimSpace(reason); reason != "" {
		metadata["reason"] = reason
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeAgentBlocked),
		Message:   message,
		Metadata:  metadata,
		Timestamp: e.now().UTC(),
	})
	return true
}

func (e *kimiStreamProgressEmitter) emitTokenUsage(usage map[string]json.RawMessage) bool {
	if len(usage) == 0 {
		return false
	}
	metadata := map[string]string{}
	for key, raw := range usage {
		value, ok := kimiJSONNumberString(raw)
		if !ok {
			continue
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		return false
	}
	e.emit(contracts.RunnerProgress{
		Type:      string(contracts.EventTypeTokenUsage),
		Metadata:  metadata,
		Timestamp: e.now().UTC(),
	})
	return true
}

func kimiInputString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := input[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func kimiToolTarget(input map[string]any) string {
	return kimiInputString(input, "file_path", "path", "notebook_path", "url", "pattern")
}

func resultText(content kimiStreamContent) string {
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

var kimiExitCodePattern = regexp.MustCompile(`(?im)\b(?:exit[_ ]code|Exit code)\s*:?\s*(-?\d+)\b`)
var kimiDurationPattern = regexp.MustCompile(`(?im)\b(?:duration[_ ]ms|duration)\s*:?\s*([0-9]+(?:\.[0-9]+)?)\s*(ms|millisecond(?:s)?|s|sec|second(?:s)?)?\b`)

func kimiExtractExitCode(text string) (int, bool) {
	matches := kimiExitCodePattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, false
	}
	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

func kimiExtractDurationMS(text string) (int64, bool) {
	matches := kimiDurationPattern.FindStringSubmatch(text)
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

func kimiJSONNumberString(raw json.RawMessage) (string, bool) {
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
