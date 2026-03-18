package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/version"
)

type TaskSessionRuntime struct {
	binary  string
	args    []string
	starter AppServerStarter
}

const defaultAppServerShutdownGracePeriod = time.Second

func NewTaskSessionRuntime(binary string, args ...string) *TaskSessionRuntime {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = defaultBinary
	}
	normalizedArgs := append([]string(nil), args...)
	return &TaskSessionRuntime{
		binary:  resolvedBinary,
		args:    normalizedArgs,
		starter: appServerStarterFunc(startAppServerProcess),
	}
}

type AppServerTaskSession struct {
	id         string
	repoRoot   string
	proc       appServerProcess
	reader     *jsonRPCPayloadReader
	writer     io.WriteCloser
	stderrDone chan struct{}
	waitDone   chan struct{}

	closeOnce sync.Once
	closeErr  error

	writeMu   sync.Mutex
	stateMu   sync.Mutex
	stderrErr error
	waitErr   error
	threadID  string
	turnID    string
	nextID    int64
}

func (r *TaskSessionRuntime) Start(ctx context.Context, request contracts.TaskSessionStartRequest) (_ contracts.TaskSession, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil, errors.New("nil codex task session runtime")
	}

	spec := CommandSpec{
		Binary: r.binary,
		Args:   r.buildArgs(request),
		Env:    flattenSessionEnv(request.Env),
		Dir:    strings.TrimSpace(request.RepoRoot),
	}
	proc, err := nonNilAppServerStarter(r.starter).Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if proc == nil {
		return nil, errors.New("codex app-server starter returned nil process")
	}

	session := &AppServerTaskSession{
		id:         taskSessionID(request),
		repoRoot:   strings.TrimSpace(request.RepoRoot),
		proc:       proc,
		reader:     newJSONRPCPayloadReader(proc.Stdout()),
		writer:     proc.Stdin(),
		stderrDone: make(chan struct{}),
		waitDone:   make(chan struct{}),
	}
	go func() {
		_, copyErr := io.Copy(io.Discard, proc.Stderr())
		session.stderrErr = copyErr
		close(session.stderrDone)
	}()
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()

	defer func() {
		if err == nil {
			return
		}
		err = errors.Join(err, session.close(true))
	}()

	// The runtime only establishes the app-server session here; task turns start later.
	if _, err = session.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "yolo-runner",
			"version": version.Version,
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		return nil, err
	}

	if err = session.send(contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, err
	}

	return session, nil
}

func (r *TaskSessionRuntime) buildArgs(request contracts.TaskSessionStartRequest) []string {
	if len(request.Command) > 0 {
		return append([]string(nil), request.Command...)
	}
	if len(r.args) > 0 {
		return append([]string(nil), r.args...)
	}
	return []string{"app-server"}
}

func flattenSessionEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+env[key])
	}
	return values
}

func taskSessionID(request contracts.TaskSessionStartRequest) string {
	if id := strings.TrimSpace(request.TaskID); id != "" {
		return id
	}
	return "codex-app-server"
}

func (s *AppServerTaskSession) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *AppServerTaskSession) WaitReady(context.Context) error {
	if s == nil {
		return errors.New("nil codex app-server task session")
	}
	return nil
}

func (s *AppServerTaskSession) Execute(ctx context.Context, request contracts.TaskSessionExecuteRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil codex app-server task session")
	}

	threadResp, completed, err := s.callWithExecute(ctx, request, "thread/start", map[string]any{
		"approvalPolicy": "never",
		"cwd":            s.repoRoot,
		"ephemeral":      true,
		"model":          strings.TrimSpace(request.Model),
		"sandbox":        "danger-full-access",
		"personality":    "pragmatic",
	})
	if err != nil {
		return err
	}

	threadID := lookupString(lookupMap(threadResp.Result, "thread"), "id")
	if threadID == "" {
		threadID = lookupString(threadResp.Result, "threadId", "thread_id")
	}
	if threadID == "" {
		return errors.New("codex app-server thread/start response missing thread id")
	}
	s.setThread(threadID)
	if completed {
		return nil
	}

	turnResp, completed, err := s.callWithExecute(ctx, request, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{
				"type": "text",
				"text": strings.TrimSpace(request.Prompt),
			},
		},
	})
	if err != nil {
		return err
	}
	s.setTurn(lookupString(lookupMap(turnResp.Result, "turn"), "id"), threadID)
	if completed {
		return nil
	}

	for {
		message, err := s.readMessage(ctx)
		if err != nil {
			return err
		}
		completed, err := s.handleExecuteMessage(ctx, request, message)
		if err != nil {
			return err
		}
		if completed {
			return nil
		}
	}
}

func (s *AppServerTaskSession) Cancel(ctx context.Context, request contracts.TaskSessionCancellation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil codex app-server task session")
	}
	if request.Force {
		return s.close(true)
	}
	threadID, turnID := s.activeTurn()
	if threadID == "" || turnID == "" {
		return nil
	}
	return ignoreClosedPipeError(s.send(contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      s.nextRequestID(),
		Method:  "turn/interrupt",
		Params: map[string]any{
			"threadId": threadID,
			"turnId":   turnID,
		},
	}))
}

func (s *AppServerTaskSession) Teardown(ctx context.Context, request contracts.TaskSessionTeardown) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil codex app-server task session")
	}
	if request.Force {
		return s.close(true)
	}
	shutdownErr := ignoreClosedPipeError(s.send(contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      s.nextRequestID(),
		Method:  "shutdown",
	}))
	timer := time.NewTimer(defaultAppServerShutdownGracePeriod)
	defer timer.Stop()
	select {
	case <-s.waitDone:
		return errors.Join(shutdownErr, s.close(false))
	case <-timer.C:
		return errors.Join(shutdownErr, s.close(true))
	case <-ctx.Done():
		return errors.Join(shutdownErr, s.close(true))
	}
}

func (s *AppServerTaskSession) close(force bool) error {
	s.closeOnce.Do(func() {
		if s.writer != nil {
			s.closeErr = errors.Join(s.closeErr, ignoreClosedPipeError(s.writer.Close()))
		}
		if force {
			s.closeErr = errors.Join(s.closeErr, ignoreProcessDoneError(s.proc.Kill()))
		}
		<-s.stderrDone
		<-s.waitDone
		s.closeErr = errors.Join(s.closeErr, ignoreClosedPipeError(s.stderrErr))
		s.closeErr = errors.Join(s.closeErr, ignoreProcessDoneError(s.waitErr))
	})
	return s.closeErr
}

func (s *AppServerTaskSession) call(ctx context.Context, method string, params map[string]any) (contracts.JSONRPCMessage, error) {
	message, _, err := s.callWithExecute(ctx, contracts.TaskSessionExecuteRequest{}, method, params)
	return message, err
}

func (s *AppServerTaskSession) callWithExecute(ctx context.Context, request contracts.TaskSessionExecuteRequest, method string, params map[string]any) (contracts.JSONRPCMessage, bool, error) {
	if s == nil {
		return contracts.JSONRPCMessage{}, false, errors.New("nil codex app-server task session")
	}
	id := s.nextRequestID()
	if err := s.send(contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		return contracts.JSONRPCMessage{}, false, err
	}
	completed := false
	for {
		message, err := s.readMessage(ctx)
		if err != nil {
			return contracts.JSONRPCMessage{}, completed, err
		}
		if strings.TrimSpace(message.Method) != "" {
			nextCompleted, err := s.handleExecuteMessage(ctx, request, message)
			if nextCompleted {
				completed = true
			}
			if err != nil {
				return contracts.JSONRPCMessage{}, completed, err
			}
			continue
		}
		if normalizeJSONID(message.ID) != normalizeJSONID(id) {
			continue
		}
		if message.Error != nil {
			return contracts.JSONRPCMessage{}, completed, fmt.Errorf("codex app-server %s failed: %s", method, message.Error.Message)
		}
		return message, completed, nil
	}
}

func (s *AppServerTaskSession) handleExecuteMessage(ctx context.Context, request contracts.TaskSessionExecuteRequest, message contracts.JSONRPCMessage) (bool, error) {
	s.trackLifecycle(message)
	if event, completion, ok := NormalizeAppServerNotification(message, request.Mode); ok {
		if request.EventSink != nil {
			if err := request.EventSink.HandleEvent(ctx, event); err != nil {
				return false, err
			}
		}
		if completion != nil {
			return true, nil
		}
	}

	if strings.TrimSpace(message.Method) == "" || len(message.ID) == 0 {
		return false, nil
	}

	response, err := buildAppServerRequestResponse(ctx, message, request)
	if err != nil {
		return false, err
	}
	if response == nil {
		return false, nil
	}
	if err := s.send(contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      message.ID,
		Result:  response,
	}); err != nil {
		return false, err
	}
	return false, nil
}

func (s *AppServerTaskSession) nextRequestID() json.RawMessage {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.nextID++
	return json.RawMessage(strconv.FormatInt(s.nextID, 10))
}

func (s *AppServerTaskSession) send(message contracts.JSONRPCMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return sendJSONRPCMessage(s.writer, message)
}

func (s *AppServerTaskSession) setThread(threadID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if strings.TrimSpace(threadID) != "" {
		s.threadID = strings.TrimSpace(threadID)
	}
}

func (s *AppServerTaskSession) setTurn(turnID string, fallbackThreadID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if strings.TrimSpace(fallbackThreadID) != "" && strings.TrimSpace(s.threadID) == "" {
		s.threadID = strings.TrimSpace(fallbackThreadID)
	}
	if strings.TrimSpace(turnID) != "" {
		s.turnID = strings.TrimSpace(turnID)
	}
}

func (s *AppServerTaskSession) activeTurn() (string, string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.threadID, s.turnID
}

func (s *AppServerTaskSession) trackLifecycle(message contracts.JSONRPCMessage) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	threadID := lookupString(message.Params, "threadId", "thread_id")
	turnID := lookupString(message.Params, "turnId", "turn_id")
	if threadID != "" {
		s.threadID = threadID
	}
	switch strings.TrimSpace(message.Method) {
	case "turn/started":
		if turnID != "" {
			s.turnID = turnID
		}
	case "turn/completed", "turn/failed", "turn/cancelled", "turn/interrupted":
		s.turnID = ""
	}
}

func buildAppServerRequestResponse(ctx context.Context, message contracts.JSONRPCMessage, request contracts.TaskSessionExecuteRequest) (map[string]any, error) {
	switch strings.TrimSpace(message.Method) {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		if request.ApprovalHandler == nil {
			return defaultAppServerRequestResponse(message)
		}
		event, _, ok := NormalizeAppServerNotification(message, request.Mode)
		if !ok || event.Approval == nil {
			return defaultAppServerRequestResponse(message)
		}
		decision, err := request.ApprovalHandler.HandleApproval(ctx, event.Approval.Request)
		if err != nil {
			return nil, err
		}
		switch decision.Outcome {
		case contracts.TaskSessionApprovalRejected:
			return map[string]any{"decision": "reject"}, nil
		case contracts.TaskSessionApprovalDeferred:
			return map[string]any{"decision": "abort"}, nil
		default:
			return map[string]any{"decision": "accept"}, nil
		}
	case "item/tool/requestUserInput":
		if request.QuestionHandler == nil {
			return defaultAppServerRequestResponse(message)
		}
		answers := map[string]any{}
		for _, question := range lookupSlice(message.Params, "questions") {
			mapped, ok := question.(map[string]any)
			if !ok {
				continue
			}
			questionID := lookupString(mapped, "id")
			if questionID == "" {
				continue
			}
			options := make([]string, 0, len(lookupSlice(mapped, "options")))
			for _, option := range lookupSlice(mapped, "options") {
				optionMap, ok := option.(map[string]any)
				if !ok {
					continue
				}
				if label := lookupString(optionMap, "label"); label != "" {
					options = append(options, label)
				}
			}
			response, err := request.QuestionHandler.HandleQuestion(ctx, contracts.TaskSessionQuestionRequest{
				ID:       questionID,
				Prompt:   lookupString(mapped, "question"),
				Context:  lookupString(message.Params, "title"),
				Options:  options,
				Metadata: cloneStringMap(nil),
				Payload:  mapped,
			})
			if err != nil {
				return nil, err
			}
			answer := strings.TrimSpace(response.Answer)
			if answer == "" {
				answer = firstQuestionAnswer(mapped)
			}
			answers[questionID] = map[string]any{"answers": []string{answer}}
		}
		return map[string]any{"answers": answers}, nil
	default:
		return defaultAppServerRequestResponse(message)
	}
}

func defaultAppServerRequestResponse(message contracts.JSONRPCMessage) (map[string]any, error) {
	response, ok := appServerRequestResponse(message)
	if !ok {
		return nil, nil
	}
	return response, nil
}

func (s *AppServerTaskSession) readMessage(ctx context.Context) (contracts.JSONRPCMessage, error) {
	type result struct {
		payload []byte
		err     error
	}

	readDone := make(chan result, 1)
	go func() {
		payload, err := s.reader.Read()
		readDone <- result{payload: payload, err: err}
	}()

	select {
	case <-ctx.Done():
		return contracts.JSONRPCMessage{}, ctx.Err()
	case next := <-readDone:
		if next.err != nil {
			return contracts.JSONRPCMessage{}, next.err
		}
		var message contracts.JSONRPCMessage
		if err := json.Unmarshal(next.payload, &message); err != nil {
			return contracts.JSONRPCMessage{}, err
		}
		return message, nil
	}
}

func ignoreClosedPipeError(err error) error {
	if err == nil || isIgnorableAppServerPipeError(err) {
		return nil
	}
	return err
}

func ignoreProcessDoneError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "process already finished") {
		return nil
	}
	return err
}
