package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ClientSideConnection implements the Client interface for making requests to the client.
type ClientSideConnection struct {
	conn *Connection
	impl Client
}

// NewClientSideConnection creates a new client-side connection to an agent.
//
// This establishes the communication channel from the client's perspective
// following the ACP specification.
//
// Parameters:
//   - client: The Client implementation that will handle incoming agent requests
//   - reader: The stream for receiving data from the agent (typically agent's stdout)
//   - writer: The stream for sending data to the agent (typically agent's stdin)
//
// See protocol docs: [Communication Model](https://agentclientprotocol.com/protocol/overview#communication-model)
func NewClientSideConnection(client Client, writer io.Writer, reader io.Reader) *ClientSideConnection {
	csc := &ClientSideConnection{
		impl: client,
	}

	// Create bidirectional JSON-RPC connection
	handler := func(method string, params json.RawMessage) (any, error) {
		return csc.handleIncomingMethod(method, params)
	}
	csc.conn = NewConnection(handler, reader, writer)

	return csc
}

// SessionUpdate sends a session update notification to the client.
func (c *ClientSideConnection) SessionUpdate(ctx context.Context, params *SessionNotification) error {
	return c.conn.SendNotification(ctx, ClientMethods.SessionUpdate, params)
}

// RequestPermission requests user permission for a tool call operation.
func (c *ClientSideConnection) RequestPermission(ctx context.Context, params *RequestPermissionRequest) (*RequestPermissionResponse, error) {
	data, err := c.conn.SendRequest(ctx, ClientMethods.SessionRequestPermission, params)
	if err != nil {
		return nil, err
	}
	var response RequestPermissionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ReadTextFile reads content from a text file in the client's file system.
func (c *ClientSideConnection) ReadTextFile(ctx context.Context, params *ReadTextFileRequest) (*ReadTextFileResponse, error) {
	data, err := c.conn.SendRequest(ctx, ClientMethods.FsReadTextFile, params)
	if err != nil {
		return nil, err
	}
	var response ReadTextFileResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// WriteTextFile writes content to a text file in the client's file system.
func (c *ClientSideConnection) WriteTextFile(ctx context.Context, params *WriteTextFileRequest) error {
	_, err := c.conn.SendRequest(ctx, ClientMethods.FsWriteTextFile, params)
	return err
}

// CreateTerminal creates a new terminal session.
func (c *ClientSideConnection) CreateTerminal(ctx context.Context, params *CreateTerminalRequest) (*CreateTerminalResponse, error) {
	data, err := c.conn.SendRequest(ctx, ClientMethods.TerminalCreate, params)
	if err != nil {
		return nil, err
	}
	var response CreateTerminalResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// CreateTerminalHandle executes a command in a new terminal and returns a TerminalHandle.
//
// Returns a TerminalHandle that can be used to get output, wait for exit,
// kill the command, or release the terminal.
//
// The terminal can also be embedded in tool calls by using its ID in
// ToolCallContent with type "terminal".
//
// This matches the TypeScript AgentSideConnection.createTerminal() method.
func (c *ClientSideConnection) CreateTerminalHandle(ctx context.Context, params *CreateTerminalRequest) (*TerminalHandle, error) {
	response, err := c.CreateTerminal(ctx, params)
	if err != nil {
		return nil, err
	}

	return NewTerminalHandle(response.TerminalId, params.SessionId, c.conn), nil
}

// TerminalOutput gets the current output and status of a terminal.
func (c *ClientSideConnection) TerminalOutput(ctx context.Context, params *TerminalOutputRequest) (*TerminalOutputResponse, error) {
	data, err := c.conn.SendRequest(ctx, ClientMethods.TerminalOutput, params)
	if err != nil {
		return nil, err
	}
	var response TerminalOutputResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ReleaseTerminal releases a terminal and frees its resources.
func (c *ClientSideConnection) ReleaseTerminal(ctx context.Context, params *ReleaseTerminalRequest) error {
	_, err := c.conn.SendRequest(ctx, ClientMethods.TerminalRelease, params)
	return err
}

// WaitForTerminalExit waits for a terminal command to exit.
func (c *ClientSideConnection) WaitForTerminalExit(ctx context.Context, params *WaitForTerminalExitRequest) (*WaitForTerminalExitResponse, error) {
	data, err := c.conn.SendRequest(ctx, ClientMethods.TerminalWaitForExit, params)
	if err != nil {
		return nil, err
	}
	var response WaitForTerminalExitResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// KillTerminalCommand kills a terminal command without releasing the terminal.
func (c *ClientSideConnection) KillTerminalCommand(ctx context.Context, params *KillTerminalCommandRequest) error {
	_, err := c.conn.SendRequest(ctx, ClientMethods.TerminalKill, params)
	return err
}

// Agent protocol methods - these are called by the agent

// Initialize starts the connection handshake with the agent
func (c *ClientSideConnection) Initialize(ctx context.Context, params *InitializeRequest) (*InitializeResponse, error) {
	data, err := c.conn.SendRequest(ctx, AgentMethods.Initialize, params)
	if err != nil {
		return nil, err
	}
	var response InitializeResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// Authenticate performs authentication with the agent
func (c *ClientSideConnection) Authenticate(ctx context.Context, params *AuthenticateRequest) error {
	_, err := c.conn.SendRequest(ctx, AgentMethods.Authenticate, params)
	return err
}

// NewSession creates a new conversation session with the agent
func (c *ClientSideConnection) NewSession(ctx context.Context, params *NewSessionRequest) (*NewSessionResponse, error) {
	data, err := c.conn.SendRequest(ctx, AgentMethods.SessionNew, params)
	if err != nil {
		return nil, err
	}
	var response NewSessionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// LoadSession loads an existing session (if supported)
func (c *ClientSideConnection) LoadSession(ctx context.Context, params *LoadSessionRequest) (*LoadSessionResponse, error) {
	data, err := c.conn.SendRequest(ctx, AgentMethods.SessionLoad, params)
	if err != nil {
		return nil, err
	}
	var response LoadSessionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// SetSessionMode changes session mode (unstable)
func (c *ClientSideConnection) SetSessionMode(ctx context.Context, params *SetSessionModeRequest) error {
	_, err := c.conn.SendRequest(ctx, AgentMethods.SessionSetMode, params)
	return err
}

// Prompt sends a user prompt to the agent
func (c *ClientSideConnection) Prompt(ctx context.Context, params *PromptRequest) (*PromptResponse, error) {
	data, err := c.conn.SendRequest(ctx, AgentMethods.SessionPrompt, params)
	if err != nil {
		return nil, err
	}
	var response PromptResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// Cancel sends a cancellation notification to the agent
func (c *ClientSideConnection) Cancel(ctx context.Context, params *CancelNotification) error {
	return c.conn.SendNotification(ctx, AgentMethods.SessionCancel, params)
}

// handleIncomingMethod handles incoming JSON-RPC method calls from the agent
func (c *ClientSideConnection) handleIncomingMethod(method string, params json.RawMessage) (any, error) {
	ctx := context.Background() // TODO: Add proper context handling

	switch method {
	case ClientMethods.SessionUpdate:
		var sessionNotification SessionNotification
		if err := json.Unmarshal(params, &sessionNotification); err != nil {
			return nil, err
		}
		return nil, c.impl.SessionUpdate(ctx, &sessionNotification)

	case ClientMethods.SessionRequestPermission:
		var permissionRequest RequestPermissionRequest
		if err := json.Unmarshal(params, &permissionRequest); err != nil {
			return nil, err
		}
		return c.impl.RequestPermission(ctx, &permissionRequest)

	case ClientMethods.FsReadTextFile:
		var readRequest ReadTextFileRequest
		if err := json.Unmarshal(params, &readRequest); err != nil {
			return nil, err
		}
		return c.impl.ReadTextFile(ctx, &readRequest)

	case ClientMethods.FsWriteTextFile:
		var writeRequest WriteTextFileRequest
		if err := json.Unmarshal(params, &writeRequest); err != nil {
			return nil, err
		}
		err := c.impl.WriteTextFile(ctx, &writeRequest)
		return nil, err

	case ClientMethods.TerminalCreate:
		var terminalRequest CreateTerminalRequest
		if err := json.Unmarshal(params, &terminalRequest); err != nil {
			return nil, err
		}
		return c.impl.CreateTerminal(ctx, &terminalRequest)

	case ClientMethods.TerminalOutput:
		var outputRequest TerminalOutputRequest
		if err := json.Unmarshal(params, &outputRequest); err != nil {
			return nil, err
		}
		return c.impl.TerminalOutput(ctx, &outputRequest)

	case ClientMethods.TerminalRelease:
		var releaseRequest ReleaseTerminalRequest
		if err := json.Unmarshal(params, &releaseRequest); err != nil {
			return nil, err
		}
		return nil, c.impl.ReleaseTerminal(ctx, &releaseRequest)

	case ClientMethods.TerminalWaitForExit:
		var waitRequest WaitForTerminalExitRequest
		if err := json.Unmarshal(params, &waitRequest); err != nil {
			return nil, err
		}
		return c.impl.WaitForTerminalExit(ctx, &waitRequest)

	case ClientMethods.TerminalKill:
		var killRequest KillTerminalCommandRequest
		if err := json.Unmarshal(params, &killRequest); err != nil {
			return nil, err
		}
		return nil, c.impl.KillTerminalCommand(ctx, &killRequest)

	default:
		return nil, fmt.Errorf("method not found: %s", method)
	}
}

// Start begins processing JSON-RPC messages
func (c *ClientSideConnection) Start(ctx context.Context) error {
	return c.conn.Start(ctx)
}
