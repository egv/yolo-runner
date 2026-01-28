package opencode

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	acp "github.com/ironpark/acp-go"
)

const verificationPrompt = "Verify task completion: run required tests if not already run, then reply with DONE or NOT DONE."
const verificationFailureReason = "verification did not confirm completion"
const verificationIdleDelay = 200 * time.Millisecond

type ACPDecision string

const (
	ACPDecisionAllow  ACPDecision = "allow"
	ACPDecisionDecide ACPDecision = "decide"
)

type ACPHandler struct {
	issueID string
	logPath string
	logger  func(string, string, string, string) error
}

func NewACPHandler(issueID string, logPath string, logger func(string, string, string, string) error) *ACPHandler {
	return &ACPHandler{issueID: issueID, logPath: logPath, logger: logger}
}

func (h *ACPHandler) HandlePermission(ctx context.Context, requestID string, scope string) ACPDecision {
	if h != nil && h.logger != nil {
		_ = h.logger(h.logPath, h.issueID, "permission", "allow")
	}
	return ACPDecisionAllow
}

func (h *ACPHandler) HandleQuestion(ctx context.Context, requestID string, prompt string) string {
	if h != nil && h.logger != nil {
		_ = h.logger(h.logPath, h.issueID, "question", "decide yourself")
	}
	return "decide yourself"
}

func RunACPClient(
	ctx context.Context,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
	repoRoot string,
	prompt string,
	handler *ACPHandler,
	onUpdate func(*acp.SessionNotification),
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if stdin == nil || stdout == nil {
		return errors.New("acp client requires stdin and stdout")
	}
	client := &acpClient{handler: handler, onUpdate: onUpdate}
	connection := acp.NewClientSideConnection(client, stdin, stdout)

	errCh := make(chan error, 1)
	go func() {
		errCh <- connection.Start(ctx)
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

	newSession := func() (*acp.NewSessionResponse, error) {
		session, err := connection.NewSession(ctx, &acp.NewSessionRequest{
			Cwd:        repoRoot,
			McpServers: []acp.McpServer{},
		})
		if err != nil {
			return nil, err
		}
		if modeID := findModeID(session.Modes, "yolo"); modeID != "" {
			if err := connection.SetSessionMode(ctx, &acp.SetSessionModeRequest{
				ModeId:    modeID,
				SessionId: session.SessionId,
			}); err != nil {
				return nil, err
			}
		}
		return session, nil
	}

	promptFn := func(sessionId acp.SessionId) func(string) error {
		return func(text string) error {
			_, err := connection.Prompt(ctx, &acp.PromptRequest{
				SessionId: sessionId,
				Prompt: []acp.ContentBlock{
					acp.NewContentBlockText(text),
				},
			})
			return err
		}
	}

	runPrompt := func(sessionId acp.SessionId, text string) (string, error) {
		client.startCapture()
		if err := promptFn(sessionId)(text); err != nil {
			return "", err
		}
		client.waitForCaptureIdle(ctx, verificationIdleDelay)
		response := client.stopCapture()
		if err := sendQuestionResponses(ctx, promptFn(sessionId), client.drainQuestionResponses()); err != nil {
			return "", err
		}
		return response, nil
	}

	cancelSession := func(sessionId acp.SessionId) {
		_ = connection.Cancel(ctx, &acp.CancelNotification{SessionId: sessionId})
	}

	runOnce := func() (bool, error) {
		session, err := newSession()
		if err != nil {
			return false, err
		}
		if _, err := runPrompt(session.SessionId, prompt); err != nil {
			return false, err
		}
		cancelSession(session.SessionId)

		verifySession, err := newSession()
		if err != nil {
			return false, err
		}
		verificationText, err := runPrompt(verifySession.SessionId, verificationPrompt)
		if err != nil {
			return false, err
		}
		cancelSession(verifySession.SessionId)
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
		verified, err = runOnce()
		if err != nil {
			return err
		}
		if !verified {
			return &VerificationError{Reason: verificationFailureReason}
		}
	}

	client.closeQuestionResponses()
	_ = stdin.Close()

	if err := <-errCh; err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	return nil
}

func sendQuestionResponses(ctx context.Context, promptFn func(string) error, responses []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if promptFn == nil || len(responses) == 0 {
		return nil
	}
	for _, response := range responses {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if response == "" {
			continue
		}
		if err := promptFn(response); err != nil {
			return err
		}
	}
	return nil
}

func parseVerificationResponse(text string) (bool, bool) {
	if text == "" {
		return false, false
	}
	normalized := strings.ToLower(text)
	if strings.Contains(normalized, "not done") || strings.Contains(normalized, "not complete") || strings.Contains(normalized, "incomplete") {
		return false, true
	}
	if strings.Contains(normalized, "did not") || strings.Contains(normalized, "didn't") || strings.Contains(normalized, "failed") || strings.Contains(normalized, "fail") {
		return false, true
	}
	if strings.Contains(normalized, "done") {
		return true, true
	}
	if strings.Contains(normalized, "complete") {
		return true, true
	}
	if strings.Contains(normalized, "pass") {
		return true, true
	}
	return false, false
}

type acpClient struct {
	handler                 *ACPHandler
	onUpdate                func(*acp.SessionNotification)
	questionResponses       []string
	questionResponsesMu     sync.Mutex
	questionResponsesClosed bool
	captureMu               sync.Mutex
	captureBuffer           strings.Builder
	captureEnabled          bool
	captureStartedAt        time.Time
	captureLastUpdate       time.Time
}

func (c *acpClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	if c != nil && c.onUpdate != nil {
		c.onUpdate(params)
	}
	if c != nil {
		c.captureMessage(params)
	}
	return nil
}

func (c *acpClient) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	isQuestion := false
	if params.ToolCall.Kind != nil && strings.EqualFold(string(*params.ToolCall.Kind), "question") {
		isQuestion = true
	}
	if strings.Contains(strings.ToLower(params.ToolCall.Title), "question") {
		isQuestion = true
	}
	if isQuestion {
		response := ""
		if c != nil && c.handler != nil {
			response = c.handler.HandleQuestion(ctx, string(params.ToolCall.ToolCallId), params.ToolCall.Title)
		}
		if response != "" && c != nil {
			c.enqueueQuestionResponse(response)
		}
		return &acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeCancelled(),
		}, nil
	}

	decision := ACPDecisionAllow
	if c != nil && c.handler != nil {
		decision = c.handler.HandlePermission(ctx, string(params.ToolCall.ToolCallId), params.ToolCall.Title)
	}

	if decision != ACPDecisionAllow || len(params.Options) == 0 {
		return &acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeCancelled(),
		}, nil
	}

	var option acp.PermissionOption
	found := false
	for i := range params.Options {
		candidate := params.Options[i]
		if candidate.Kind == acp.PermissionOptionKindAllowOnce || candidate.Kind == acp.PermissionOptionKindAllowAlways {
			option = candidate
			found = true
			break
		}
	}
	if !found {
		return &acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeCancelled(),
		}, nil
	}
	return &acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId),
	}, nil
}

func (c *acpClient) startCapture() {
	if c == nil {
		return
	}
	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	c.captureBuffer.Reset()
	c.captureEnabled = true
	c.captureStartedAt = time.Now()
	c.captureLastUpdate = time.Time{}
}

func (c *acpClient) stopCapture() string {
	if c == nil {
		return ""
	}
	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	c.captureEnabled = false
	return c.captureBuffer.String()
}

func (c *acpClient) captureMessage(params *acp.SessionNotification) {
	if c == nil || params == nil {
		return
	}
	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	if !c.captureEnabled {
		return
	}
	content := ""
	if update := params.Update.GetAgentmessagechunk(); update != nil {
		if update.Content.IsText() {
			content = update.Content.GetText().Text
		}
	} else if update := params.Update.GetAgentthoughtchunk(); update != nil {
		if update.Content.IsText() {
			content = update.Content.GetText().Text
		}
	}
	if content == "" {
		return
	}
	c.captureBuffer.WriteString(content)
	c.captureLastUpdate = time.Now()
}

func (c *acpClient) waitForCaptureIdle(ctx context.Context, idle time.Duration) {
	if c == nil || idle <= 0 {
		return
	}
	start := time.Now()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.captureMu.Lock()
			last := c.captureLastUpdate
			started := c.captureStartedAt
			c.captureMu.Unlock()
			if !last.IsZero() {
				if time.Since(last) >= idle {
					return
				}
				continue
			}
			if time.Since(started) >= idle && time.Since(start) >= idle {
				return
			}
		}
	}
}

func (c *acpClient) closeQuestionResponses() {
	if c == nil {
		return
	}
	c.questionResponsesMu.Lock()
	defer c.questionResponsesMu.Unlock()
	if c.questionResponsesClosed {
		return
	}
	c.questionResponsesClosed = true
}

func (c *acpClient) enqueueQuestionResponse(response string) {
	if c == nil || response == "" {
		return
	}
	c.questionResponsesMu.Lock()
	defer c.questionResponsesMu.Unlock()
	if c.questionResponsesClosed {
		return
	}
	c.questionResponses = append(c.questionResponses, response)
}

func (c *acpClient) drainQuestionResponses() []string {
	if c == nil {
		return nil
	}
	c.questionResponsesMu.Lock()
	defer c.questionResponsesMu.Unlock()
	if len(c.questionResponses) == 0 {
		return nil
	}
	responses := append([]string(nil), c.questionResponses...)
	c.questionResponses = nil
	return responses
}

func (c *acpClient) ReadTextFile(ctx context.Context, params *acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	content, err := readFileSegment(params.Path, params.Line, params.Limit)
	if err != nil {
		return nil, err
	}
	return &acp.ReadTextFileResponse{Content: content}, nil
}

func (c *acpClient) WriteTextFile(ctx context.Context, params *acp.WriteTextFileRequest) error {
	return os.WriteFile(params.Path, []byte(params.Content), 0o644)
}

func (c *acpClient) CreateTerminal(ctx context.Context, params *acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	return nil, errors.New("terminal support disabled")
}

func (c *acpClient) TerminalOutput(ctx context.Context, params *acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	return nil, errors.New("terminal support disabled")
}

func (c *acpClient) ReleaseTerminal(ctx context.Context, params *acp.ReleaseTerminalRequest) error {
	return errors.New("terminal support disabled")
}

func (c *acpClient) WaitForTerminalExit(ctx context.Context, params *acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	return nil, errors.New("terminal support disabled")
}

func (c *acpClient) KillTerminalCommand(ctx context.Context, params *acp.KillTerminalCommandRequest) error {
	return errors.New("terminal support disabled")
}

func findModeID(state *acp.SessionModeState, name string) acp.SessionModeId {
	if state == nil {
		return ""
	}
	for _, mode := range state.AvailableModes {
		if strings.EqualFold(string(mode.Id), name) || strings.EqualFold(mode.Name, name) {
			return mode.Id
		}
	}
	return ""
}

func readFileSegment(path string, line *int64, limit *int64) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if line == nil && limit == nil {
		return string(content), nil
	}

	lines := strings.Split(string(content), "\n")
	start := 0
	if line != nil && *line > 0 {
		start = int(*line)
	}
	if start >= len(lines) {
		return "", nil
	}

	end := len(lines)
	if limit != nil && *limit >= 0 {
		end = start + int(*limit)
		if end > len(lines) {
			end = len(lines)
		}
	}

	return strings.Join(lines[start:end], "\n"), nil
}
