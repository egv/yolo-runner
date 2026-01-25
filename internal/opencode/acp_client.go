package opencode

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	acp "github.com/ironpark/acp-go"
)

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

	session, err := connection.NewSession(ctx, &acp.NewSessionRequest{
		Cwd:        repoRoot,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		return err
	}

	promptFn := func(text string) error {
		_, err := connection.Prompt(ctx, &acp.PromptRequest{
			SessionId: session.SessionId,
			Prompt: []acp.ContentBlock{
				acp.NewContentBlockText(text),
			},
		})
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

	_, err = connection.Prompt(ctx, &acp.PromptRequest{
		SessionId: session.SessionId,
		Prompt: []acp.ContentBlock{
			acp.NewContentBlockText(prompt),
		},
	})
	if err != nil {
		return err
	}
	client.closeQuestionResponses()
	if err := sendQuestionResponses(ctx, promptFn, client.drainQuestionResponses()); err != nil {
		return err
	}
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

type acpClient struct {
	handler                 *ACPHandler
	onUpdate                func(*acp.SessionNotification)
	questionResponses       []string
	questionResponsesMu     sync.Mutex
	questionResponsesClosed bool
}

func (c *acpClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	if c != nil && c.onUpdate != nil {
		c.onUpdate(params)
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
