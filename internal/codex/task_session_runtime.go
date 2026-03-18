package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/version"
)

type TaskSessionRuntime struct {
	binary  string
	args    []string
	starter AppServerStarter
}

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
	proc       appServerProcess
	reader     *jsonRPCPayloadReader
	writer     io.WriteCloser
	stderrDone chan error

	closeOnce sync.Once
	closeErr  error
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
		proc:       proc,
		reader:     newJSONRPCPayloadReader(proc.Stdout()),
		writer:     proc.Stdin(),
		stderrDone: make(chan error, 1),
	}
	go func() {
		_, copyErr := io.Copy(io.Discard, proc.Stderr())
		session.stderrDone <- copyErr
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

	if err = sendJSONRPCMessage(session.writer, contracts.JSONRPCMessage{
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

func (s *AppServerTaskSession) Execute(context.Context, contracts.TaskSessionExecuteRequest) error {
	if s == nil {
		return errors.New("nil codex app-server task session")
	}
	return errors.New("codex app-server execute not implemented")
}

func (s *AppServerTaskSession) Cancel(context.Context, contracts.TaskSessionCancellation) error {
	if s == nil {
		return errors.New("nil codex app-server task session")
	}
	return s.close(true)
}

func (s *AppServerTaskSession) Teardown(context.Context, contracts.TaskSessionTeardown) error {
	if s == nil {
		return errors.New("nil codex app-server task session")
	}
	return s.close(true)
}

func (s *AppServerTaskSession) close(force bool) error {
	s.closeOnce.Do(func() {
		if s.writer != nil {
			s.closeErr = errors.Join(s.closeErr, ignoreClosedPipeError(s.writer.Close()))
		}
		if force {
			s.closeErr = errors.Join(s.closeErr, ignoreProcessDoneError(s.proc.Kill()))
		}
		s.closeErr = errors.Join(s.closeErr, ignoreClosedPipeError(<-s.stderrDone))
		s.closeErr = errors.Join(s.closeErr, ignoreProcessDoneError(s.proc.Wait()))
	})
	return s.closeErr
}

func (s *AppServerTaskSession) call(ctx context.Context, method string, params map[string]any) (contracts.JSONRPCMessage, error) {
	if s == nil {
		return contracts.JSONRPCMessage{}, errors.New("nil codex app-server task session")
	}
	if err := sendJSONRPCMessage(s.writer, contracts.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  method,
		Params:  params,
	}); err != nil {
		return contracts.JSONRPCMessage{}, err
	}
	for {
		message, err := s.readMessage(ctx)
		if err != nil {
			return contracts.JSONRPCMessage{}, err
		}
		if strings.TrimSpace(message.Method) != "" {
			response, ok := appServerRequestResponse(message)
			if !ok {
				continue
			}
			if err := sendJSONRPCMessage(s.writer, contracts.JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      message.ID,
				Result:  response,
			}); err != nil {
				return contracts.JSONRPCMessage{}, err
			}
			continue
		}
		if normalizeJSONID(message.ID) != "1" {
			continue
		}
		if message.Error != nil {
			return contracts.JSONRPCMessage{}, fmt.Errorf("codex app-server %s failed: %s", method, message.Error.Message)
		}
		return message, nil
	}
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
