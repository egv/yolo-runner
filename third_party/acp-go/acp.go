package acp

//go:generate sh -c "cd internal/cmd/schema && go run . gen -config ../../../.schema.yaml"

import "context"

// Agent represents the interface that agents must implement to handle client requests.
//
// This interface defines all the methods that an agent can receive from a client
// according to the Agent Client Protocol specification.
//
// See protocol docs: [Agent](https://agentclientprotocol.com/protocol/overview#agent)
type Agent interface {
	// Initialize establishes the connection and negotiates capabilities.
	//
	// This is the first method called after connection establishment.
	// The agent should return its capabilities and supported protocol version.
	//
	// See protocol docs: [Initialization](https://agentclientprotocol.com/protocol/initialization)
	Initialize(ctx context.Context, params *InitializeRequest) (*InitializeResponse, error)

	// Authenticate handles user authentication using the specified method.
	//
	// Called when the client needs to authenticate the user with the agent.
	//
	// See protocol docs: [Authentication](https://agentclientprotocol.com/protocol/authentication)
	Authenticate(ctx context.Context, params *AuthenticateRequest) error

	// NewSession creates a new conversation session.
	//
	// Sets up a new session with the specified working directory and MCP servers.
	//
	// See protocol docs: [Creating a Session](https://agentclientprotocol.com/protocol/session-setup#creating-a-session)
	NewSession(ctx context.Context, params *NewSessionRequest) (*NewSessionResponse, error)

	// LoadSession loads an existing conversation session (optional).
	//
	// Only called if the agent advertises the `loadSession` capability.
	//
	// See protocol docs: [Loading Sessions](https://agentclientprotocol.com/protocol/session-setup#loading-sessions)
	LoadSession(ctx context.Context, params *LoadSessionRequest) (*LoadSessionResponse, error)

	// SetSessionMode changes the current session mode (optional, unstable).
	//
	// This method is not part of the stable spec and may be removed or changed.
	SetSessionMode(ctx context.Context, params *SetSessionModeRequest) error

	// Prompt processes a user prompt and generates a response.
	//
	// This is the main method for handling user input and generating agent responses.
	//
	// See protocol docs: [User Message](https://agentclientprotocol.com/protocol/prompt-turn#1-user-message)
	Prompt(ctx context.Context, params *PromptRequest) (*PromptResponse, error)

	// Cancel cancels ongoing operations for a session.
	//
	// This is a notification method (no response expected).
	//
	// See protocol docs: [Cancellation](https://agentclientprotocol.com/protocol/prompt-turn#cancellation)
	Cancel(ctx context.Context, params *CancelNotification) error
}

// Client represents the interface for communicating with the client from an agent.
//
// This interface provides methods that agents can use to request services
// from the client, such as file system access and permission requests.
//
// See protocol docs: [Client](https://agentclientprotocol.com/protocol/overview#client)
type Client interface {
	// SessionUpdate sends a session update notification to the client.
	//
	// Used to stream real-time progress and results during prompt processing.
	//
	// See protocol docs: [Agent Reports Output](https://agentclientprotocol.com/protocol/prompt-turn#3-agent-reports-output)
	SessionUpdate(ctx context.Context, params *SessionNotification) error

	// RequestPermission requests user permission for a tool call operation.
	//
	// Called when the agent needs user authorization before executing
	// a potentially sensitive operation.
	//
	// See protocol docs: [Requesting Permission](https://agentclientprotocol.com/protocol/tool-calls#requesting-permission)
	RequestPermission(ctx context.Context, params *RequestPermissionRequest) (*RequestPermissionResponse, error)

	// ReadTextFile reads content from a text file in the client's file system.
	//
	// Only available if the client supports the `fs.readTextFile` capability.
	//
	// See protocol docs: [FileSystem](https://agentclientprotocol.com/protocol/initialization#filesystem)
	ReadTextFile(ctx context.Context, params *ReadTextFileRequest) (*ReadTextFileResponse, error)

	// WriteTextFile writes content to a text file in the client's file system.
	//
	// Only available if the client supports the `fs.writeTextFile` capability.
	//
	// See protocol docs: [FileSystem](https://agentclientprotocol.com/protocol/initialization#filesystem)
	WriteTextFile(ctx context.Context, params *WriteTextFileRequest) error

	// CreateTerminal creates a new terminal session (unstable).
	//
	// This method is not part of the stable spec and may be removed or changed.
	CreateTerminal(ctx context.Context, params *CreateTerminalRequest) (*CreateTerminalResponse, error)

	// TerminalOutput gets the current output and status of a terminal (unstable).
	TerminalOutput(ctx context.Context, params *TerminalOutputRequest) (*TerminalOutputResponse, error)

	// ReleaseTerminal releases a terminal and frees its resources (unstable).
	ReleaseTerminal(ctx context.Context, params *ReleaseTerminalRequest) error

	// WaitForTerminalExit waits for a terminal command to exit (unstable).
	WaitForTerminalExit(ctx context.Context, params *WaitForTerminalExitRequest) (*WaitForTerminalExitResponse, error)

	// KillTerminalCommand kills a terminal command without releasing the terminal (unstable).
	KillTerminalCommand(ctx context.Context, params *KillTerminalCommandRequest) error
}
