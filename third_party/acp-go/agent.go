package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// AgentSideConnection represents an agent-side connection to a client.
//
// This class provides the agent's view of an ACP connection, allowing
// agents to communicate with clients. It implements the Client interface
// to provide methods for requesting permissions, accessing the file system,
// and sending session updates.
//
// See protocol docs: [Agent](https://agentclientprotocol.com/protocol/overview#agent)
type AgentSideConnection struct {
	conn   *Connection
	agent  Agent
	client *ClientSideConnection
}

// NewAgentSideConnection creates a new agent-side connection to a client.
//
// This establishes the communication channel from the agent's perspective
// following the ACP specification.
//
// Parameters:
//   - agent: The Agent implementation that will handle incoming client requests
//   - reader: The stream for receiving data from the client (typically stdin)
//   - writer: The stream for sending data to the client (typically stdout)
//
// See protocol docs: [Communication Model](https://agentclientprotocol.com/protocol/overview#communication-model)
func NewAgentSideConnection(agent Agent, reader io.Reader, writer io.Writer) *AgentSideConnection {
	asc := &AgentSideConnection{
		agent: agent,
	}

	// Create bidirectional JSON-RPC connection
	handler := func(method string, params json.RawMessage) (any, error) {
		return asc.handleIncomingMethod(method, params)
	}
	asc.conn = NewConnection(handler, reader, writer)

	// Create client interface for making requests to the client
	asc.client = &ClientSideConnection{
		conn: asc.conn,
		impl: nil, // This will be set by the user
	}

	return asc
}

// Client returns the client interface for making requests to the client.
func (c *AgentSideConnection) Client() Client {
	return c.client
}

// Start starts the connection and begins processing messages.
func (c *AgentSideConnection) Start(ctx context.Context) error {
	return c.conn.Start(ctx)
}

// handleIncomingMethod handles incoming JSON-RPC method calls from the client
func (c *AgentSideConnection) handleIncomingMethod(method string, params json.RawMessage) (any, error) {
	ctx := context.Background() // TODO: Add proper context handling

	switch method {
	case AgentMethods.Initialize:
		var req InitializeRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		return c.agent.Initialize(ctx, &req)

	case AgentMethods.Authenticate:
		var req AuthenticateRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		err := c.agent.Authenticate(ctx, &req)
		return nil, err

	case AgentMethods.SessionNew:
		var req NewSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		return c.agent.NewSession(ctx, &req)

	case AgentMethods.SessionLoad:
		var req LoadSessionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		return c.agent.LoadSession(ctx, &req)

	case AgentMethods.SessionSetMode:
		var req SetSessionModeRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		err := c.agent.SetSessionMode(ctx, &req)
		return nil, err

	case AgentMethods.SessionPrompt:
		var req PromptRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		return c.agent.Prompt(ctx, &req)

	case AgentMethods.SessionCancel:
		var req CancelNotification
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("invalid parameters: %w", err)
		}
		err := c.agent.Cancel(ctx, &req)
		return nil, err

	default:
		return nil, fmt.Errorf("method not found: %s", method)
	}
}
