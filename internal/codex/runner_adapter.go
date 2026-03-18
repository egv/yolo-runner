package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/egv/yolo-runner/v2/internal/version"
)

const defaultBinary = "codex"

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

type appServerProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() error
	Kill() error
}

type AppServerStarter interface {
	Start(context.Context, CommandSpec) (appServerProcess, error)
}

type appServerStarterFunc func(context.Context, CommandSpec) (appServerProcess, error)

func (f appServerStarterFunc) Start(ctx context.Context, spec CommandSpec) (appServerProcess, error) {
	return f(ctx, spec)
}

type CLIRunnerAdapter struct {
	binary string
	args   []string
	runner CommandRunner
	starter AppServerStarter
	now    func() time.Time
}

func NewCLIRunnerAdapter(binary string, runner CommandRunner, args ...string) *CLIRunnerAdapter {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = defaultBinary
	}
	normalizedArgs := append([]string(nil), args...)
	return &CLIRunnerAdapter{
		binary: resolvedBinary,
		args:   normalizedArgs,
		runner: runner,
		starter: appServerStarterFunc(startAppServerProcess),
		now:    time.Now,
	}
}

func (a *CLIRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		return contracts.RunnerResult{}, errors.New("nil codex runner adapter")
	}
	if a.runner == nil {
		a.starter = nonNilAppServerStarter(a.starter)
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

	protocolPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogProtocolTrace)
	protocolFile, err := os.Create(protocolPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer protocolFile.Close()

	runCtx, cancel := contracts.WithOptionalTimeout(ctx, request.Timeout)
	defer cancel()

	var completion *AppServerCompletion
	var runErr error
	if a.runner != nil {
		runErr, completion = a.runLegacyLineMode(runCtx, request, stdoutFile, stderrFile, protocolFile)
	} else {
		runErr, completion = a.runAppServerMode(runCtx, request, stdoutFile, stderrFile, protocolFile)
	}
	runErr = contracts.FinalizeRunError(runCtx, runErr)

	finishedAt := a.now().UTC()
	result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)
	result.LogPath = logPath
	ApplyAppServerCompletion(&result, completion)
	hasCompletion := completion != nil
	hasCompletionVerdict := completion != nil && completion.HasReviewVerdict
	result.Artifacts = buildRunnerArtifacts(request, result)
	ApplyAppServerCompletion(&result, completion)
	if result.Status == contracts.RunnerResultCompleted && request.Mode == contracts.RunnerModeReview && (!hasCompletion || !hasCompletionVerdict) {
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
		return filepath.Join(request.RepoRoot, "runner-logs", "codex", request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", "codex", request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", "codex", "codex-run.jsonl")
}

func (a *CLIRunnerAdapter) buildArgs(request contracts.RunnerRequest) []string {
	if len(a.args) > 0 {
		return resolveBackendArgs(a.args, "codex", request)
	}
	return defaultBuildArgs(request)
}

func defaultBuildArgs(request contracts.RunnerRequest) []string {
	return []string{"app-server"}
}

func resolveBackendArgs(raw []string, backend string, request contracts.RunnerRequest) []string {
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = "codex"
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
		return errors.New("codex binary is required")
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

func nonNilAppServerStarter(starter AppServerStarter) AppServerStarter {
	if starter != nil {
		return starter
	}
	return appServerStarterFunc(startAppServerProcess)
}

func (a *CLIRunnerAdapter) runLegacyLineMode(ctx context.Context, request contracts.RunnerRequest, stdoutFile *os.File, stderrFile *os.File, protocolFile *os.File) (error, *AppServerCompletion) {
	var completionMu sync.Mutex
	var completion *AppServerCompletion

	emitProgress := func(source string, line string) {
		if message, ok := decodeJSONRPCNotification(line); ok {
			completionMu.Lock()
			_, _ = protocolFile.WriteString(strings.TrimRight(line, "\r") + "\n")
			progress, nextCompletion, ok := RunnerProgressFromAppServerNotification(message, request.Mode)
			if ok && request.OnProgress != nil {
				request.OnProgress(progress)
			}
			if nextCompletion != nil {
				completion = nextCompletion
			}
			completionMu.Unlock()
			return
		}
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

	runErr := a.runner.Run(ctx, CommandSpec{
		Binary: a.binary,
		Args:   a.buildArgs(request),
		Dir:    request.RepoRoot,
		Stdout: stdoutWriter,
		Stderr: stderrWriter,
	})
	stdoutWriter.Flush()
	stderrWriter.Flush()

	completionMu.Lock()
	defer completionMu.Unlock()
	return runErr, completion
}

func (a *CLIRunnerAdapter) runAppServerMode(ctx context.Context, request contracts.RunnerRequest, stdoutFile *os.File, stderrFile *os.File, protocolFile *os.File) (runErr error, completion *AppServerCompletion) {
	spec := CommandSpec{
		Binary: a.binary,
		Args:   a.buildArgs(request),
		Dir:    request.RepoRoot,
	}
	proc, err := nonNilAppServerStarter(a.starter).Start(ctx, spec)
	if err != nil {
		return err, nil
	}

	stderrWriter := newLineWriter(stderrFile, func(line string) {
		if request.OnProgress == nil {
			return
		}
		progress, ok := contracts.NewRunnerOutputProgress("stderr", line, a.now().UTC())
		if ok {
			request.OnProgress(progress)
		}
	})
	stderrDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stderrWriter, proc.Stderr())
		stderrWriter.Flush()
		stderrDone <- copyErr
	}()
	defer func() {
		if copyErr := <-stderrDone; copyErr != nil && !errors.Is(copyErr, io.EOF) && !isIgnorableAppServerPipeError(copyErr) {
			runErr = errors.Join(runErr, copyErr)
		}
	}()
	defer func() {
		killErr := proc.Kill()
		waitErr := proc.Wait()
		if runErr == nil {
			if killErr != nil {
				runErr = killErr
			} else if waitErr != nil {
				runErr = waitErr
			}
			return
		}
		runErr = errors.Join(runErr, killErr, waitErr)
	}()

	reader := newJSONRPCPayloadReader(proc.Stdout())
	writer := proc.Stdin()
	defer writer.Close()

	nextID := 1
	call := func(method string, params map[string]any) (contracts.JSONRPCMessage, error) {
		id := json.RawMessage(strconv.Itoa(nextID))
		nextID++
		if err := sendJSONRPCMessage(writer, contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
			Method:  method,
			Params:  params,
		}); err != nil {
			return contracts.JSONRPCMessage{}, err
		}
		for {
			msg, err := a.readAppServerMessage(reader, stdoutFile, protocolFile)
			if err != nil {
				return contracts.JSONRPCMessage{}, err
			}
			if strings.TrimSpace(msg.Method) != "" {
				nextCompletion, handleErr := a.handleAppServerMessage(ctx, writer, request, msg)
				if nextCompletion != nil {
					completion = nextCompletion
				}
				if handleErr != nil {
					return contracts.JSONRPCMessage{}, handleErr
				}
				continue
			}
			if normalizeJSONID(msg.ID) != normalizeJSONID(id) {
				continue
			}
			if msg.Error != nil {
				return contracts.JSONRPCMessage{}, fmt.Errorf("codex app-server %s failed: %s", method, msg.Error.Message)
			}
			return msg, nil
		}
	}

	if _, err := call("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "yolo-runner",
			"version": version.Version,
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		return err, completion
	}
	if err := sendJSONRPCMessage(writer, contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return err, completion
	}

	threadResp, err := call("thread/start", map[string]any{
		"approvalPolicy": "never",
		"cwd":            strings.TrimSpace(request.RepoRoot),
		"ephemeral":      true,
		"model":          strings.TrimSpace(request.Model),
		"sandbox":        "danger-full-access",
		"personality":    "pragmatic",
	})
	if err != nil {
		return err, completion
	}
	threadID := lookupString(lookupMap(threadResp.Result, "thread"), "id")
	if threadID == "" {
		threadID = lookupString(threadResp.Result, "threadId", "thread_id")
	}
	if threadID == "" {
		return errors.New("codex app-server thread/start response missing thread id"), completion
	}

	if _, err := call("turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{
				"type": "text",
				"text": strings.TrimSpace(request.Prompt),
			},
		},
	}); err != nil {
		return err, completion
	}

	for completion == nil {
		msg, err := a.readAppServerMessage(reader, stdoutFile, protocolFile)
		if err != nil {
			return err, completion
		}
		nextCompletion, handleErr := a.handleAppServerMessage(ctx, writer, request, msg)
		if nextCompletion != nil {
			completion = nextCompletion
		}
		if handleErr != nil {
			return handleErr, completion
		}
	}

	return nil, completion
}

func (a *CLIRunnerAdapter) readAppServerMessage(reader *jsonRPCPayloadReader, stdoutFile *os.File, protocolFile *os.File) (contracts.JSONRPCMessage, error) {
	payload, err := reader.Read()
	if err != nil {
		return contracts.JSONRPCMessage{}, err
	}
	text := strings.TrimSpace(string(payload))
	if text == "" {
		return contracts.JSONRPCMessage{}, io.EOF
	}
	_, _ = stdoutFile.WriteString(text + "\n")
	_, _ = protocolFile.WriteString(text + "\n")
	var message contracts.JSONRPCMessage
	if err := json.Unmarshal([]byte(text), &message); err != nil {
		return contracts.JSONRPCMessage{}, err
	}
	return message, nil
}

func (a *CLIRunnerAdapter) handleAppServerMessage(ctx context.Context, writer io.Writer, request contracts.RunnerRequest, message contracts.JSONRPCMessage) (*AppServerCompletion, error) {
	if progress, nextCompletion, ok := RunnerProgressFromAppServerNotification(message, request.Mode); ok {
		if request.OnProgress != nil {
			request.OnProgress(progress)
		}
		if nextCompletion != nil {
			return nextCompletion, nil
		}
	}
	response, ok := appServerRequestResponse(message)
	if !ok {
		return nil, nil
	}
	if err := sendJSONRPCMessage(writer, contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      message.ID,
		Result:  response,
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func appServerRequestResponse(message contracts.JSONRPCMessage) (map[string]any, bool) {
	if strings.TrimSpace(message.Method) == "" || len(message.ID) == 0 {
		return nil, false
	}
	switch strings.TrimSpace(message.Method) {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return map[string]any{"decision": "accept"}, true
	case "item/tool/requestUserInput":
		answers := map[string]any{}
		for _, question := range lookupSlice(message.Params, "questions") {
			q, ok := question.(map[string]any)
			if !ok {
				continue
			}
			questionID := lookupString(q, "id")
			if questionID == "" {
				continue
			}
			selection := firstQuestionAnswer(q)
			answers[questionID] = map[string]any{"answers": []string{selection}}
		}
		return map[string]any{"answers": answers}, true
	default:
		return nil, false
	}
}

func firstQuestionAnswer(question map[string]any) string {
	options := lookupSlice(question, "options")
	for _, option := range options {
		mapped, ok := option.(map[string]any)
		if !ok {
			continue
		}
		if label := lookupString(mapped, "label"); label != "" {
			return label
		}
	}
	return "Proceed"
}

func sendJSONRPCMessage(writer io.Writer, message contracts.JSONRPCMessage) error {
	if writer == nil {
		return errors.New("json-rpc writer is nil")
	}
	if strings.TrimSpace(message.JSONRPC) == "" {
		message.JSONRPC = "2.0"
	}
	return json.NewEncoder(writer).Encode(message)
}

func normalizeJSONID(id json.RawMessage) string {
	return strings.TrimSpace(string(id))
}

type jsonRPCPayloadReader struct {
	reader *bufio.Reader
}

func newJSONRPCPayloadReader(reader io.Reader) *jsonRPCPayloadReader {
	return &jsonRPCPayloadReader{reader: bufio.NewReader(reader)}
}

func (r *jsonRPCPayloadReader) Read() ([]byte, error) {
	if r == nil || r.reader == nil {
		return nil, io.EOF
	}
	var payload bytes.Buffer
	depth := 0
	inString := false
	escaped := false
	started := false
	for {
		b, err := r.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if !started {
			if isJSONWhitespace(b) {
				continue
			}
			if b != '{' && b != '[' {
				return nil, fmt.Errorf("unexpected json-rpc payload prefix %q", b)
			}
			started = true
		}
		payload.WriteByte(b)
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return payload.Bytes(), nil
			}
		}
	}
}

func isJSONWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func isIgnorableAppServerPipeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "file already closed")
}

type osAppServerProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func startAppServerProcess(ctx context.Context, spec CommandSpec) (appServerProcess, error) {
	if strings.TrimSpace(spec.Binary) == "" {
		return nil, errors.New("codex binary is required")
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	if strings.TrimSpace(spec.Dir) != "" {
		cmd.Dir = spec.Dir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osAppServerProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (p *osAppServerProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *osAppServerProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *osAppServerProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *osAppServerProcess) Wait() error          { return p.cmd.Wait() }
func (p *osAppServerProcess) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
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
	return contracts.BuildRunnerArtifacts("codex", request, result, extras)
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

func decodeJSONRPCNotification(line string) (contracts.JSONRPCMessage, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return contracts.JSONRPCMessage{}, false
	}
	var message contracts.JSONRPCMessage
	if err := json.Unmarshal([]byte(trimmed), &message); err != nil {
		return contracts.JSONRPCMessage{}, false
	}
	if strings.TrimSpace(message.Method) == "" {
		return contracts.JSONRPCMessage{}, false
	}
	return message, true
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
