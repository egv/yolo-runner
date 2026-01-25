package opencode

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

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

func RunACPClient(ctx context.Context, endpoint string, repoRoot string, prompt string, handler *ACPHandler) error {
	if ctx == nil {
		ctx = context.Background()
	}

	address := endpoint
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		address = parsed.Host
	}

	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := &acpClient{handler: handler}
	connection := acp.NewClientSideConnection(client, conn, conn)

	errCh := make(chan error, 1)
	go func() {
		errCh <- connection.Start(ctx)
	}()

	_, err = connection.Initialize(ctx, &acp.InitializeRequest{
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

	if err := <-errCh; err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	return nil
}

type acpClient struct {
	handler *ACPHandler
}

func (c *acpClient) SessionUpdate(ctx context.Context, params *acp.SessionNotification) error {
	return nil
}

func (c *acpClient) RequestPermission(ctx context.Context, params *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	decision := ACPDecisionAllow
	if c != nil && c.handler != nil {
		decision = c.handler.HandlePermission(ctx, string(params.ToolCall.ToolCallId), params.ToolCall.Title)
	}

	if decision != ACPDecisionAllow || len(params.Options) == 0 {
		return &acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeCancelled(),
		}, nil
	}

	option := params.Options[0]
	return &acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId),
	}, nil
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
