package opencode

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	acp "github.com/ironpark/acp-go"
)

// ACPTaskSessionRuntime implements contracts.TaskSessionRuntime for the opencode
// ACP protocol. Each Start() call launches the ACP process (opencode acp) with
// piped stdin/stdout, and WaitReady() establishes the ACP connection handshake.
type ACPTaskSessionRuntime struct {
	runner Runner
}

// NewACPTaskSessionRuntime returns a runtime that spawns the ACP process using
// the given runner and establishes the ACP connection during WaitReady.
func NewACPTaskSessionRuntime(runner Runner) *ACPTaskSessionRuntime {
	return &ACPTaskSessionRuntime{runner: runner}
}

var _ contracts.TaskSessionRuntime = (*ACPTaskSessionRuntime)(nil)

// stdioProcess is the process interface required by the ACP runtime. The
// process must expose stdin/stdout pipes for the ACP wire protocol.
type stdioProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
}

// Start launches the ACP process and returns an ACPTaskSession. The session is
// not yet connected; call WaitReady to perform the ACP Initialize handshake.
func (r *ACPTaskSessionRuntime) Start(ctx context.Context, request contracts.TaskSessionStartRequest) (contracts.TaskSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil, errors.New("nil ACP task session runtime")
	}

	logPath := resolveACPSessionLogPath(request)
	if err := os.MkdirAll(resolveACPLogDir(logPath), 0o755); err != nil {
		return nil, err
	}

	args := resolveACPStartArgs(request)
	env := BuildEnv(request.Env, "", "", "")

	proc, err := r.runner.Start(args, env, logPath)
	if err != nil {
		return nil, err
	}

	stdio, ok := proc.(stdioProcess)
	if !ok {
		_ = proc.Kill()
		return nil, errors.New("ACP process does not expose stdin/stdout pipes")
	}

	session := &ACPTaskSession{
		id:          resolveACPTaskSessionID(request),
		repoRoot:    strings.TrimSpace(request.RepoRoot),
		proc:        proc,
		stdin:       stdio.Stdin(),
		stdout:      stdio.Stdout(),
		stopTimeout: request.StopTimeout,
		waitDone:    make(chan struct{}),
	}
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()
	return session, nil
}

// defaultACPStopTimeout is the grace period given to the ACP process to exit
// on its own after stdin/stdout are closed before it is forcibly killed.
const defaultACPStopTimeout = 5 * time.Second

// ACPTaskSession is a running ACP session backed by a single OS process.
type ACPTaskSession struct {
	id          string
	repoRoot    string
	proc        Process
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stopTimeout time.Duration

	readyOnce  sync.Once
	readyErr   error
	connection *acp.ClientSideConnection
	acpCli     *acpClient
	sessionID  acp.SessionId
	startErrCh chan error

	closeOnce sync.Once
	closeErr  error

	waitDone chan struct{}
	waitErr  error
}

// ID returns the task session identifier.
func (s *ACPTaskSession) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// WaitReady establishes the ACP connection by performing the Initialize
// handshake and creating an ACP session. It is idempotent.
func (s *ACPTaskSession) WaitReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil ACP task session")
	}
	s.readyOnce.Do(func() {
		s.readyErr = s.doWaitReady(ctx)
	})
	return s.readyErr
}

func (s *ACPTaskSession) doWaitReady(ctx context.Context) error {
	cli := &acpClient{}
	connection := acp.NewClientSideConnection(cli, s.stdin, s.stdout)

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- connection.Start(ctx)
	}()

	_, err := connection.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion),
		ClientCapabilities: &acp.ClientCapabilities{
			Fs: &acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		return err
	}

	session, err := connection.NewSession(ctx, &acp.NewSessionRequest{
		Cwd:        s.repoRoot,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return err
	}

	if modeID := findModeID(session.Modes, "yolo"); modeID != "" {
		if err := connection.SetSessionMode(ctx, &acp.SetSessionModeRequest{
			ModeId:    modeID,
			SessionId: session.SessionId,
		}); err != nil {
			return err
		}
	}

	s.connection = connection
	s.acpCli = cli
	s.sessionID = session.SessionId
	s.startErrCh = startErrCh
	return nil
}

// Execute submits the prompt via ACP and waits for the session to complete.
// It calls WaitReady first if not already done.
func (s *ACPTaskSession) Execute(ctx context.Context, req contracts.TaskSessionExecuteRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil ACP task session")
	}
	if err := s.WaitReady(ctx); err != nil {
		return err
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return errors.New("ACP execute requires a non-empty prompt")
	}

	cli := s.acpCli
	conn := s.connection

	newSession := func() (acp.SessionId, error) {
		session, err := conn.NewSession(ctx, &acp.NewSessionRequest{
			Cwd:        s.repoRoot,
			McpServers: []acp.McpServer{},
		})
		if err != nil {
			return "", err
		}
		return session.SessionId, nil
	}

	promptFn := func(sessionID acp.SessionId) func(string) error {
		return func(text string) error {
			_, err := conn.Prompt(ctx, &acp.PromptRequest{
				SessionId: sessionID,
				Prompt:    []acp.ContentBlock{acp.NewContentBlockText(text)},
			})
			return err
		}
	}

	runPrompt := func(sessionID acp.SessionId, text string) (string, error) {
		fn := promptFn(sessionID)
		cli.startCapture()
		if err := fn(text); err != nil {
			return "", err
		}
		cli.signalPromptCompleted()
		cli.waitForCaptureIdle(ctx, verificationIdleDelay)
		response := cli.stopCapture()
		if err := sendQuestionResponses(ctx, fn, cli.drainQuestionResponses()); err != nil {
			return "", err
		}
		return response, nil
	}

	runOnce := func() (bool, error) {
		taskSessionID := s.sessionID
		if _, err := runPrompt(taskSessionID, prompt); err != nil {
			return false, err
		}
		conn.Cancel(ctx, &acp.CancelNotification{SessionId: taskSessionID})

		verifySessionID, err := newSession()
		if err != nil {
			return false, err
		}
		verificationText, err := runPrompt(verifySessionID, verificationPrompt)
		if err != nil {
			return false, err
		}
		conn.Cancel(ctx, &acp.CancelNotification{SessionId: verifySessionID})

		verified, ok := parseVerificationResponse(verificationText)
		if !ok || !verified {
			return false, nil
		}
		return true, nil
	}

	verified, err := runOnce()
	if err != nil {
		return err
	}
	if !verified {
		// Create a fresh session for the retry.
		retrySessionID, err := newSession()
		if err != nil {
			return err
		}
		s.sessionID = retrySessionID
		verified, err = runOnce()
		if err != nil {
			return err
		}
		if !verified {
			return &VerificationError{Reason: verificationFailureReason}
		}
	}

	cli.closeQuestionResponses()

	_ = conn.Close()
	_ = s.stdin.Close()
	_ = s.stdout.Close()
	const shutdownGrace = 250 * time.Millisecond
	select {
	case <-s.startErrCh:
	case <-time.After(shutdownGrace):
	}
	return nil
}

// Cancel stops the session, optionally forcing immediate termination.
func (s *ACPTaskSession) Cancel(ctx context.Context, req contracts.TaskSessionCancellation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil ACP task session")
	}
	return s.close(req.Force)
}

// Teardown cleans up the session, optionally forcing immediate termination.
func (s *ACPTaskSession) Teardown(ctx context.Context, req contracts.TaskSessionTeardown) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil ACP task session")
	}
	return s.close(req.Force)
}

func (s *ACPTaskSession) close(force bool) error {
	s.closeOnce.Do(func() {
		_ = s.stdin.Close()
		_ = s.stdout.Close()
		if force {
			s.closeErr = ignoreServeProcessDone(s.proc.Kill())
			<-s.waitDone
			return
		}
		// Graceful: wait for the process to exit after pipe closure, then fall
		// back to Kill if it does not exit within the stop timeout.
		timeout := s.stopTimeout
		if timeout <= 0 {
			timeout = defaultACPStopTimeout
		}
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-s.waitDone:
		case <-timer.C:
			s.closeErr = ignoreServeProcessDone(s.proc.Kill())
			<-s.waitDone
		}
	})
	return s.closeErr
}

// resolveACPTaskSessionID returns the task ID or a default session identifier.
func resolveACPTaskSessionID(request contracts.TaskSessionStartRequest) string {
	if id := strings.TrimSpace(request.TaskID); id != "" {
		return id
	}
	return "opencode-acp"
}

// resolveACPSessionLogPath returns the log path for the ACP session.
func resolveACPSessionLogPath(request contracts.TaskSessionStartRequest) string {
	if path := strings.TrimSpace(request.LogPath); path != "" {
		return path
	}
	if strings.TrimSpace(request.RepoRoot) != "" && strings.TrimSpace(request.TaskID) != "" {
		return request.RepoRoot + "/runner-logs/opencode/" + request.TaskID + ".jsonl"
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return "runner-logs/opencode/" + request.TaskID + ".jsonl"
	}
	return "runner-logs/opencode/opencode-acp.jsonl"
}

func resolveACPLogDir(logPath string) string {
	for i := len(logPath) - 1; i >= 0; i-- {
		if logPath[i] == '/' || logPath[i] == '\\' {
			return logPath[:i]
		}
	}
	return "."
}

// resolveACPStartArgs returns the command args for the ACP process.
func resolveACPStartArgs(request contracts.TaskSessionStartRequest) []string {
	if len(request.Command) > 0 {
		return append([]string(nil), request.Command...)
	}
	model := ""
	if request.Metadata != nil {
		model = request.Metadata["model"]
	}
	return BuildACPArgsWithModel(strings.TrimSpace(request.RepoRoot), model)
}
