package acp

import (
	"encoding/json"
	"fmt"
)

// @internal
// All possible notifications that an agent can send to a client.
//
// This enum is used internally for routing RPC notifications. You typically won't need
// to use this directly - use the notification methods on the [`Client`] trait instead.
//
// Notifications do not expect a response.
type AgentNotification SessionNotification

// Unique identifier for an authentication method.
type AuthMethodId string

// @internal
// All possible notifications that a client can send to an agent.
//
// This enum is used internally for routing RPC notifications. You typically won't need
// to use this directly - use the notification methods on the [`Agent`] trait instead.
//
// Notifications do not expect a response.
type ClientNotification CancelNotification

// Resource content that can be embedded in a message.
//
// Possible types:
// - TextResourceContents
// - BlobResourceContents
type EmbeddedResourceResource interface{}

// Unique identifier for a permission option.
type PermissionOptionId string

// The type of permission option being presented to the user.
//
// Helps clients choose appropriate icons and UI treatment.
type PermissionOptionKind string

// Priority levels for plan entries.
//
// Used to indicate the relative importance or urgency of different
// tasks in the execution plan.
// See protocol docs: [Plan Entries](https://agentclientprotocol.com/protocol/agent-plan#plan-entries)
type PlanEntryPriority string

// Status of a plan entry in the execution flow.
//
// Tracks the lifecycle of each task from planning through completion.
// See protocol docs: [Plan Entries](https://agentclientprotocol.com/protocol/agent-plan#plan-entries)
type PlanEntryStatus string

// Protocol version identifier.
//
// This version is only bumped for breaking changes.
// Non-breaking changes should be introduced via capabilities.
type ProtocolVersion int64

// The sender or recipient of messages and data in a conversation.
type Role string

// A unique identifier for a conversation session between a client and agent.
//
// Sessions maintain their own context, conversation history, and state,
// allowing multiple independent interactions with the same agent.
//
// # Example
//
// ```
// use agent_client_protocol::SessionId;
// use std::sync::Arc;
//
// let session_id = SessionId(Arc::from("sess_abc123def456"));
// ```
//
// See protocol docs: [Session ID](https://agentclientprotocol.com/protocol/session-setup#session-id)
type SessionId string

// **UNSTABLE**
//
// This type is not part of the spec, and may be removed or changed at any point.
type SessionModeId string

// Reasons why an agent stops processing a prompt turn.
//
// See protocol docs: [Stop Reasons](https://agentclientprotocol.com/protocol/prompt-turn#stop-reasons)
type StopReason string

// Unique identifier for a tool call within a session.
type ToolCallId string

// Execution status of a tool call.
//
// Tool calls progress through different statuses during their lifecycle.
//
// See protocol docs: [Status](https://agentclientprotocol.com/protocol/tool-calls#status)
type ToolCallStatus string

// Categories of tools that can be invoked.
//
// Tool kinds help clients choose appropriate icons and optimize how they
// display tool execution progress.
//
// See protocol docs: [Creating](https://agentclientprotocol.com/protocol/tool-calls#creating)
type ToolKind string

const (
	// Current protocol version from metadata
	CurrentProtocolVersion int = 1
)

const (
	// Allow this operation only this time.
	PermissionOptionKindAllowOnce PermissionOptionKind = "allow_once"
	// Allow this operation and remember the choice.
	PermissionOptionKindAllowAlways PermissionOptionKind = "allow_always"
	// Reject this operation only this time.
	PermissionOptionKindRejectOnce PermissionOptionKind = "reject_once"
	// Reject this operation and remember the choice.
	PermissionOptionKindRejectAlways PermissionOptionKind = "reject_always"
)

const (
	// High priority task - critical to the overall goal.
	PlanEntryPriorityHigh PlanEntryPriority = "high"
	// Medium priority task - important but not critical.
	PlanEntryPriorityMedium PlanEntryPriority = "medium"
	// Low priority task - nice to have but not essential.
	PlanEntryPriorityLow PlanEntryPriority = "low"
)

const (
	// The task has not started yet.
	PlanEntryStatusPending PlanEntryStatus = "pending"
	// The task is currently being worked on.
	PlanEntryStatusInProgress PlanEntryStatus = "in_progress"
	// The task has been successfully completed.
	PlanEntryStatusCompleted PlanEntryStatus = "completed"
)

const (
	RoleAssistant Role = "assistant"
	RoleUser      Role = "user"
)

const (
	// The turn ended successfully.
	StopReasonEndTurn StopReason = "end_turn"
	// The turn ended because the agent reached the maximum number of tokens.
	StopReasonMaxTokens StopReason = "max_tokens"
	// The turn ended because the agent reached the maximum number of allowed
	// agent requests between user turns.
	StopReasonMaxTurnRequests StopReason = "max_turn_requests"
	// The turn ended because the agent refused to continue. The user prompt
	// and everything that comes after it won't be included in the next
	// prompt, so this should be reflected in the UI.
	StopReasonRefusal StopReason = "refusal"
	// The turn was cancelled by the client via `session/cancel`.
	//
	// This stop reason MUST be returned when the client sends a `session/cancel`
	// notification, even if the cancellation causes exceptions in underlying operations.
	// Agents should catch these exceptions and return this semantically meaningful
	// response to confirm successful cancellation.
	StopReasonCancelled StopReason = "cancelled"
)

const (
	// The tool call hasn't started running yet because the input is either
	// streaming or we're awaiting approval.
	ToolCallStatusPending ToolCallStatus = "pending"
	// The tool call is currently running.
	ToolCallStatusInProgress ToolCallStatus = "in_progress"
	// The tool call completed successfully.
	ToolCallStatusCompleted ToolCallStatus = "completed"
	// The tool call failed with an error.
	ToolCallStatusFailed ToolCallStatus = "failed"
)

const (
	// Reading files or data.
	ToolKindRead ToolKind = "read"
	// Modifying files or content.
	ToolKindEdit ToolKind = "edit"
	// Removing files or data.
	ToolKindDelete ToolKind = "delete"
	// Moving or renaming files.
	ToolKindMove ToolKind = "move"
	// Searching for information.
	ToolKindSearch ToolKind = "search"
	// Running commands or code.
	ToolKindExecute ToolKind = "execute"
	// Internal reasoning or planning.
	ToolKindThink ToolKind = "think"
	// Retrieving external data.
	ToolKindFetch ToolKind = "fetch"
	// **UNSTABLE**
	//
	// This tool kind is not part of the spec and may be removed at any point.
	ToolKindSwitchMode ToolKind = "switch_mode"
	// Other tool types (default).
	ToolKindOther ToolKind = "other"
)

// Capabilities supported by the agent.
//
// Advertised during initialization to inform the client about
// available features and content types.
//
// See protocol docs: [Agent Capabilities](https://agentclientprotocol.com/protocol/initialization#agent-capabilities)
type AgentCapabilities struct {
	LoadSession        bool                `json:"loadSession,omitempty"`
	McpCapabilities    *McpCapabilities    `json:"mcpCapabilities,omitempty"`
	PromptCapabilities *PromptCapabilities `json:"promptCapabilities,omitempty"`
}

// Optional annotations for the client. The client can use annotations to inform how objects are used or displayed
type Annotations struct {
	Audience     []Role   `json:"audience,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
}

// Audio provided to or from an LLM.
type AudioContent struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Data        string       `json:"data"`
	MimeType    string       `json:"mimeType"`
}

// Describes an available authentication method.
type AuthMethod struct {
	Description string       `json:"description,omitempty"`
	Id          AuthMethodId `json:"id"`
	Name        string       `json:"name"`
}

// Request parameters for the authenticate method.
//
// Specifies which authentication method to use.
type AuthenticateRequest struct {
	MethodId AuthMethodId `json:"methodId"`
}

// Information about a command.
type AvailableCommand struct {
	Description string                 `json:"description"`
	Input       *AvailableCommandInput `json:"input,omitempty"`
	Name        string                 `json:"name"`
}

// All text that was typed after the command name is provided as input.
type AvailableCommandInput struct {
	Hint string `json:"hint"`
}

// Binary resource contents.
type BlobResourceContents struct {
	Blob     string `json:"blob"`
	MimeType string `json:"mimeType,omitempty"`
	Uri      string `json:"uri"`
}

// Notification to cancel ongoing operations for a session.
//
// See protocol docs: [Cancellation](https://agentclientprotocol.com/protocol/prompt-turn#cancellation)
type CancelNotification struct {
	SessionId SessionId `json:"sessionId"`
}

// Capabilities supported by the client.
//
// Advertised during initialization to inform the agent about
// available features and methods.
//
// See protocol docs: [Client Capabilities](https://agentclientprotocol.com/protocol/initialization#client-capabilities)
type ClientCapabilities struct {
	Fs       *FileSystemCapability `json:"fs,omitempty"`
	Terminal bool                  `json:"terminal,omitempty"`
}

// Content blocks represent displayable information in the Agent Client Protocol.
//
// They provide a structured way to handle various types of user-facing content—whether
// it's text from language models, images for analysis, or embedded resources for context.
//
// Content blocks appear in:
// - User prompts sent via `session/prompt`
// - Language model output streamed through `session/update` notifications
// - Progress updates and results from tool calls
//
// This structure is compatible with the Model Context Protocol (MCP), enabling
// agents to seamlessly forward content from MCP tool outputs without transformation.
//
// See protocol docs: [Content](https://agentclientprotocol.com/protocol/content)
type ContentBlock struct {
	discriminator string
	text          *ContentBlockText
	image         *ContentBlockImage
	audio         *ContentBlockAudio
	resourcelink  *ContentBlockResourcelink
	resource      *ContentBlockResource
}

func (c ContentBlock) MarshalJSON() ([]byte, error) {
	switch c.discriminator {
	case "text":
		return json.Marshal(c.text)
	case "image":
		return json.Marshal(c.image)
	case "audio":
		return json.Marshal(c.audio)
	case "resource_link":
		return json.Marshal(c.resourcelink)
	case "resource":
		return json.Marshal(c.resource)
	}
	return nil, fmt.Errorf("no variant is set for ContentBlock")
}

func (c *ContentBlock) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}

	switch discriminator.Type {
	case "text":
		var v ContentBlockText
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		c.text = &v
		c.discriminator = "text"
		return nil
	case "image":
		var v ContentBlockImage
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		c.image = &v
		c.discriminator = "image"
		return nil
	case "audio":
		var v ContentBlockAudio
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		c.audio = &v
		c.discriminator = "audio"
		return nil
	case "resource_link":
		var v ContentBlockResourcelink
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		c.resourcelink = &v
		c.discriminator = "resource_link"
		return nil
	case "resource":
		var v ContentBlockResource
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		c.resource = &v
		c.discriminator = "resource"
		return nil
	}
	return fmt.Errorf("unknown discriminator value: %s", discriminator.Type)
}

func (c *ContentBlock) GetText() *ContentBlockText {
	return c.text
}

func (c *ContentBlock) IsText() bool {
	return c.text != nil
}

func (c *ContentBlock) GetImage() *ContentBlockImage {
	return c.image
}

func (c *ContentBlock) IsImage() bool {
	return c.image != nil
}

func (c *ContentBlock) GetAudio() *ContentBlockAudio {
	return c.audio
}

func (c *ContentBlock) IsAudio() bool {
	return c.audio != nil
}

func (c *ContentBlock) GetResourcelink() *ContentBlockResourcelink {
	return c.resourcelink
}

func (c *ContentBlock) IsResourcelink() bool {
	return c.resourcelink != nil
}

func (c *ContentBlock) GetResource() *ContentBlockResource {
	return c.resource
}

func (c *ContentBlock) IsResource() bool {
	return c.resource != nil
}

// Audio data for transcription or analysis.
//
// Requires the `audio` prompt capability when included in prompts.
type ContentBlockAudio struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Data        string       `json:"data"`
	MimeType    string       `json:"mimeType"`
	Type        string       `json:"type"`
}

// Images for visual context or analysis.
//
// Requires the `image` prompt capability when included in prompts.
type ContentBlockImage struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Data        string       `json:"data"`
	MimeType    string       `json:"mimeType"`
	Type        string       `json:"type"`
	Uri         string       `json:"uri,omitempty"`
}

// Complete resource contents embedded directly in the message.
//
// Preferred for including context as it avoids extra round-trips.
//
// Requires the `embeddedContext` prompt capability when included in prompts.
type ContentBlockResource struct {
	Annotations *Annotations             `json:"annotations,omitempty"`
	Resource    EmbeddedResourceResource `json:"resource"`
	Type        string                   `json:"type"`
}

// References to resources that the agent can access.
//
// All agents MUST support resource links in prompts.
type ContentBlockResourcelink struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Description string       `json:"description,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	Name        string       `json:"name"`
	Size        *int64       `json:"size,omitempty"`
	Title       string       `json:"title,omitempty"`
	Type        string       `json:"type"`
	Uri         string       `json:"uri"`
}

// Plain text content
//
// All agents MUST support text content blocks in prompts.
type ContentBlockText struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Text        string       `json:"text"`
	Type        string       `json:"type"`
}

// Request to create a new terminal and execute a command.
type CreateTerminalRequest struct {
	Args            []string      `json:"args,omitempty"`
	Command         string        `json:"command"`
	Cwd             string        `json:"cwd,omitempty"`
	Env             []EnvVariable `json:"env,omitempty"`
	OutputByteLimit *int64        `json:"outputByteLimit,omitempty"`
	SessionId       SessionId     `json:"sessionId"`
}

// Response containing the ID of the created terminal.
type CreateTerminalResponse struct {
	TerminalId string `json:"terminalId"`
}

// The contents of a resource, embedded into a prompt or tool call result.
type EmbeddedResource struct {
	Annotations *Annotations             `json:"annotations,omitempty"`
	Resource    EmbeddedResourceResource `json:"resource"`
}

// An environment variable to set when launching an MCP server.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// File system capabilities that a client may support.
//
// See protocol docs: [FileSystem](https://agentclientprotocol.com/protocol/initialization#filesystem)
type FileSystemCapability struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// An HTTP header to set when making requests to the MCP server.
type HttpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// An image provided to or from an LLM.
type ImageContent struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Data        string       `json:"data"`
	MimeType    string       `json:"mimeType"`
	Uri         string       `json:"uri,omitempty"`
}

// Request parameters for the initialize method.
//
// Sent by the client to establish connection and negotiate capabilities.
//
// See protocol docs: [Initialization](https://agentclientprotocol.com/protocol/initialization)
type InitializeRequest struct {
	ClientCapabilities *ClientCapabilities `json:"clientCapabilities,omitempty"`
	ProtocolVersion    ProtocolVersion     `json:"protocolVersion"`
}

// Response from the initialize method.
//
// Contains the negotiated protocol version and agent capabilities.
//
// See protocol docs: [Initialization](https://agentclientprotocol.com/protocol/initialization)
type InitializeResponse struct {
	AgentCapabilities *AgentCapabilities `json:"agentCapabilities,omitempty"`
	AuthMethods       []AuthMethod       `json:"authMethods,omitempty"`
	ProtocolVersion   ProtocolVersion    `json:"protocolVersion"`
}

// Request to kill a terminal command without releasing the terminal.
type KillTerminalCommandRequest struct {
	SessionId  SessionId `json:"sessionId"`
	TerminalId string    `json:"terminalId"`
}

// Request parameters for loading an existing session.
//
// Only available if the Agent supports the `loadSession` capability.
//
// See protocol docs: [Loading Sessions](https://agentclientprotocol.com/protocol/session-setup#loading-sessions)
type LoadSessionRequest struct {
	Cwd        string      `json:"cwd"`
	McpServers []McpServer `json:"mcpServers"`
	SessionId  SessionId   `json:"sessionId"`
}

// Response from loading an existing session.
type LoadSessionResponse struct {
	Modes *SessionModeState `json:"modes,omitempty"`
}

// MCP capabilities supported by the agent
type McpCapabilities struct {
	Http bool `json:"http,omitempty"`
	Sse  bool `json:"sse,omitempty"`
}

// Configuration for connecting to an MCP (Model Context Protocol) server.
//
// MCP servers provide tools and context that the agent can use when
// processing prompts.
//
// See protocol docs: [MCP Servers](https://agentclientprotocol.com/protocol/session-setup#mcp-servers)
type McpServer struct {
}

// Request parameters for creating a new session.
//
// See protocol docs: [Creating a Session](https://agentclientprotocol.com/protocol/session-setup#creating-a-session)
type NewSessionRequest struct {
	Cwd        string      `json:"cwd"`
	McpServers []McpServer `json:"mcpServers"`
}

// Response from creating a new session.
//
// See protocol docs: [Creating a Session](https://agentclientprotocol.com/protocol/session-setup#creating-a-session)
type NewSessionResponse struct {
	Modes     *SessionModeState `json:"modes,omitempty"`
	SessionId SessionId         `json:"sessionId"`
}

// An option presented to the user when requesting permission.
type PermissionOption struct {
	Kind     PermissionOptionKind `json:"kind"`
	Name     string               `json:"name"`
	OptionId PermissionOptionId   `json:"optionId"`
}

// An execution plan for accomplishing complex tasks.
//
// Plans consist of multiple entries representing individual tasks or goals.
// Agents report plans to clients to provide visibility into their execution strategy.
// Plans can evolve during execution as the agent discovers new requirements or completes tasks.
//
// See protocol docs: [Agent Plan](https://agentclientprotocol.com/protocol/agent-plan)
type Plan struct {
	Entries []PlanEntry `json:"entries"`
}

// A single entry in the execution plan.
//
// Represents a task or goal that the assistant intends to accomplish
// as part of fulfilling the user's request.
// See protocol docs: [Plan Entries](https://agentclientprotocol.com/protocol/agent-plan#plan-entries)
type PlanEntry struct {
	Content  string            `json:"content"`
	Priority PlanEntryPriority `json:"priority"`
	Status   PlanEntryStatus   `json:"status"`
}

// Prompt capabilities supported by the agent in `session/prompt` requests.
//
// Baseline agent functionality requires support for [`ContentBlock::Text`]
// and [`ContentBlock::ResourceLink`] in prompt requests.
//
// Other variants must be explicitly opted in to.
// Capabilities for different types of content in prompt requests.
//
// Indicates which content types beyond the baseline (text and resource links)
// the agent can process.
//
// See protocol docs: [Prompt Capabilities](https://agentclientprotocol.com/protocol/initialization#prompt-capabilities)
type PromptCapabilities struct {
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
	Image           bool `json:"image,omitempty"`
}

// Request parameters for sending a user prompt to the agent.
//
// Contains the user's message and any additional context.
//
// See protocol docs: [User Message](https://agentclientprotocol.com/protocol/prompt-turn#1-user-message)
type PromptRequest struct {
	Prompt    []ContentBlock `json:"prompt"`
	SessionId SessionId      `json:"sessionId"`
}

// Response from processing a user prompt.
//
// See protocol docs: [Check for Completion](https://agentclientprotocol.com/protocol/prompt-turn#4-check-for-completion)
type PromptResponse struct {
	StopReason StopReason `json:"stopReason"`
}

// Request to read content from a text file.
//
// Only available if the client supports the `fs.readTextFile` capability.
type ReadTextFileRequest struct {
	Limit     *int64    `json:"limit,omitempty"`
	Line      *int64    `json:"line,omitempty"`
	Path      string    `json:"path"`
	SessionId SessionId `json:"sessionId"`
}

// Response containing the contents of a text file.
type ReadTextFileResponse struct {
	Content string `json:"content"`
}

// Request to release a terminal and free its resources.
type ReleaseTerminalRequest struct {
	SessionId  SessionId `json:"sessionId"`
	TerminalId string    `json:"terminalId"`
}

// The outcome of a permission request.
type RequestPermissionOutcome struct {
	discriminator string
	cancelled     *RequestPermissionOutcomeCancelled
	selected      *RequestPermissionOutcomeSelected
}

func (r RequestPermissionOutcome) MarshalJSON() ([]byte, error) {
	switch r.discriminator {
	case "cancelled":
		return json.Marshal(r.cancelled)
	case "selected":
		return json.Marshal(r.selected)
	}
	return nil, fmt.Errorf("no variant is set for RequestPermissionOutcome")
}

func (r *RequestPermissionOutcome) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}

	switch discriminator.Outcome {
	case "cancelled":
		var v RequestPermissionOutcomeCancelled
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		r.cancelled = &v
		r.discriminator = "cancelled"
		return nil
	case "selected":
		var v RequestPermissionOutcomeSelected
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		r.selected = &v
		r.discriminator = "selected"
		return nil
	}
	return fmt.Errorf("unknown discriminator value: %s", discriminator.Outcome)
}

func (r *RequestPermissionOutcome) GetCancelled() *RequestPermissionOutcomeCancelled {
	return r.cancelled
}

func (r *RequestPermissionOutcome) IsCancelled() bool {
	return r.cancelled != nil
}

func (r *RequestPermissionOutcome) GetSelected() *RequestPermissionOutcomeSelected {
	return r.selected
}

func (r *RequestPermissionOutcome) IsSelected() bool {
	return r.selected != nil
}

// The prompt turn was cancelled before the user responded.
//
// When a client sends a `session/cancel` notification to cancel an ongoing
// prompt turn, it MUST respond to all pending `session/request_permission`
// requests with this `Cancelled` outcome.
//
// See protocol docs: [Cancellation](https://agentclientprotocol.com/protocol/prompt-turn#cancellation)
type RequestPermissionOutcomeCancelled struct {
	Outcome string `json:"outcome"`
}

// The user selected one of the provided options.
type RequestPermissionOutcomeSelected struct {
	OptionId PermissionOptionId `json:"optionId"`
	Outcome  string             `json:"outcome"`
}

// Request for user permission to execute a tool call.
//
// Sent when the agent needs authorization before performing a sensitive operation.
//
// See protocol docs: [Requesting Permission](https://agentclientprotocol.com/protocol/tool-calls#requesting-permission)
type RequestPermissionRequest struct {
	Options   []PermissionOption `json:"options"`
	SessionId SessionId          `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
}

// Response to a permission request.
type RequestPermissionResponse struct {
	Outcome RequestPermissionOutcome `json:"outcome"`
}

// A resource that the server is capable of reading, included in a prompt or tool call result.
type ResourceLink struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Description string       `json:"description,omitempty"`
	MimeType    string       `json:"mimeType,omitempty"`
	Name        string       `json:"name"`
	Size        *int64       `json:"size,omitempty"`
	Title       string       `json:"title,omitempty"`
	Uri         string       `json:"uri"`
}

// **UNSTABLE**
//
// This type is not part of the spec, and may be removed or changed at any point.
type SessionMode struct {
	Description string        `json:"description,omitempty"`
	Id          SessionModeId `json:"id"`
	Name        string        `json:"name"`
}

// **UNSTABLE**
//
// This type is not part of the spec, and may be removed or changed at any point.
type SessionModeState struct {
	AvailableModes []SessionMode `json:"availableModes"`
	CurrentModeId  SessionModeId `json:"currentModeId"`
}

// Notification containing a session update from the agent.
//
// Used to stream real-time progress and results during prompt processing.
//
// See protocol docs: [Agent Reports Output](https://agentclientprotocol.com/protocol/prompt-turn#3-agent-reports-output)
type SessionNotification struct {
	SessionId SessionId     `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// Different types of updates that can be sent during session processing.
//
// These updates provide real-time feedback about the agent's progress.
//
// See protocol docs: [Agent Reports Output](https://agentclientprotocol.com/protocol/prompt-turn#3-agent-reports-output)
type SessionUpdate struct {
	discriminator           string
	usermessagechunk        *SessionUpdateUsermessagechunk
	agentmessagechunk       *SessionUpdateAgentmessagechunk
	agentthoughtchunk       *SessionUpdateAgentthoughtchunk
	toolcall                *SessionUpdateToolcall
	toolcallupdate          *SessionUpdateToolcallupdate
	plan                    *SessionUpdatePlan
	availablecommandsupdate *SessionUpdateAvailablecommandsupdate
	currentmodeupdate       *SessionUpdateCurrentmodeupdate
}

func (s SessionUpdate) MarshalJSON() ([]byte, error) {
	switch s.discriminator {
	case "user_message_chunk":
		return json.Marshal(s.usermessagechunk)
	case "agent_message_chunk":
		return json.Marshal(s.agentmessagechunk)
	case "agent_thought_chunk":
		return json.Marshal(s.agentthoughtchunk)
	case "tool_call":
		return json.Marshal(s.toolcall)
	case "tool_call_update":
		return json.Marshal(s.toolcallupdate)
	case "plan":
		return json.Marshal(s.plan)
	case "available_commands_update":
		return json.Marshal(s.availablecommandsupdate)
	case "current_mode_update":
		return json.Marshal(s.currentmodeupdate)
	}
	return nil, fmt.Errorf("no variant is set for SessionUpdate")
}

func (s *SessionUpdate) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}

	switch discriminator.SessionUpdate {
	case "user_message_chunk":
		var v SessionUpdateUsermessagechunk
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.usermessagechunk = &v
		s.discriminator = "user_message_chunk"
		return nil
	case "agent_message_chunk":
		var v SessionUpdateAgentmessagechunk
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.agentmessagechunk = &v
		s.discriminator = "agent_message_chunk"
		return nil
	case "agent_thought_chunk":
		var v SessionUpdateAgentthoughtchunk
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.agentthoughtchunk = &v
		s.discriminator = "agent_thought_chunk"
		return nil
	case "tool_call":
		var v SessionUpdateToolcall
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.toolcall = &v
		s.discriminator = "tool_call"
		return nil
	case "tool_call_update":
		var v SessionUpdateToolcallupdate
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.toolcallupdate = &v
		s.discriminator = "tool_call_update"
		return nil
	case "plan":
		var v SessionUpdatePlan
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.plan = &v
		s.discriminator = "plan"
		return nil
	case "available_commands_update":
		var v SessionUpdateAvailablecommandsupdate
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.availablecommandsupdate = &v
		s.discriminator = "available_commands_update"
		return nil
	case "current_mode_update":
		var v SessionUpdateCurrentmodeupdate
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		s.currentmodeupdate = &v
		s.discriminator = "current_mode_update"
		return nil
	}
	return fmt.Errorf("unknown discriminator value: %s", discriminator.SessionUpdate)
}

func (s *SessionUpdate) GetUsermessagechunk() *SessionUpdateUsermessagechunk {
	return s.usermessagechunk
}

func (s *SessionUpdate) IsUsermessagechunk() bool {
	return s.usermessagechunk != nil
}

func (s *SessionUpdate) GetAgentmessagechunk() *SessionUpdateAgentmessagechunk {
	return s.agentmessagechunk
}

func (s *SessionUpdate) IsAgentmessagechunk() bool {
	return s.agentmessagechunk != nil
}

func (s *SessionUpdate) GetAgentthoughtchunk() *SessionUpdateAgentthoughtchunk {
	return s.agentthoughtchunk
}

func (s *SessionUpdate) IsAgentthoughtchunk() bool {
	return s.agentthoughtchunk != nil
}

func (s *SessionUpdate) GetToolcall() *SessionUpdateToolcall {
	return s.toolcall
}

func (s *SessionUpdate) IsToolcall() bool {
	return s.toolcall != nil
}

func (s *SessionUpdate) GetToolcallupdate() *SessionUpdateToolcallupdate {
	return s.toolcallupdate
}

func (s *SessionUpdate) IsToolcallupdate() bool {
	return s.toolcallupdate != nil
}

func (s *SessionUpdate) GetPlan() *SessionUpdatePlan {
	return s.plan
}

func (s *SessionUpdate) IsPlan() bool {
	return s.plan != nil
}

func (s *SessionUpdate) GetAvailablecommandsupdate() *SessionUpdateAvailablecommandsupdate {
	return s.availablecommandsupdate
}

func (s *SessionUpdate) IsAvailablecommandsupdate() bool {
	return s.availablecommandsupdate != nil
}

func (s *SessionUpdate) GetCurrentmodeupdate() *SessionUpdateCurrentmodeupdate {
	return s.currentmodeupdate
}

func (s *SessionUpdate) IsCurrentmodeupdate() bool {
	return s.currentmodeupdate != nil
}

// A chunk of the agent's response being streamed.
type SessionUpdateAgentmessagechunk struct {
	Content       ContentBlock `json:"content"`
	SessionUpdate string       `json:"sessionUpdate"`
}

// A chunk of the agent's internal reasoning being streamed.
type SessionUpdateAgentthoughtchunk struct {
	Content       ContentBlock `json:"content"`
	SessionUpdate string       `json:"sessionUpdate"`
}

// Available commands are ready or have changed
type SessionUpdateAvailablecommandsupdate struct {
	AvailableCommands []AvailableCommand `json:"availableCommands"`
	SessionUpdate     string             `json:"sessionUpdate"`
}

// The current mode of the session has changed
type SessionUpdateCurrentmodeupdate struct {
	CurrentModeId SessionModeId `json:"currentModeId"`
	SessionUpdate string        `json:"sessionUpdate"`
}

// The agent's execution plan for complex tasks.
// See protocol docs: [Agent Plan](https://agentclientprotocol.com/protocol/agent-plan)
type SessionUpdatePlan struct {
	Entries       []PlanEntry `json:"entries"`
	SessionUpdate string      `json:"sessionUpdate"`
}

// Notification that a new tool call has been initiated.
type SessionUpdateToolcall struct {
	Content       []ToolCallContent  `json:"content,omitempty"`
	Kind          *ToolKind          `json:"kind,omitempty"`
	Locations     []ToolCallLocation `json:"locations,omitempty"`
	RawInput      json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage    `json:"rawOutput,omitempty"`
	SessionUpdate string             `json:"sessionUpdate"`
	Status        *ToolCallStatus    `json:"status,omitempty"`
	Title         string             `json:"title"`
	ToolCallId    ToolCallId         `json:"toolCallId"`
}

// Update on the status or results of a tool call.
type SessionUpdateToolcallupdate struct {
	Content       []ToolCallContent  `json:"content,omitempty"`
	Kind          *ToolKind          `json:"kind,omitempty"`
	Locations     []ToolCallLocation `json:"locations,omitempty"`
	RawInput      json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage    `json:"rawOutput,omitempty"`
	SessionUpdate string             `json:"sessionUpdate"`
	Status        *ToolCallStatus    `json:"status,omitempty"`
	Title         string             `json:"title,omitempty"`
	ToolCallId    ToolCallId         `json:"toolCallId"`
}

// A chunk of the user's message being streamed.
type SessionUpdateUsermessagechunk struct {
	Content       ContentBlock `json:"content"`
	SessionUpdate string       `json:"sessionUpdate"`
}

// **UNSTABLE**
//
// This type is not part of the spec, and may be removed or changed at any point.
type SetSessionModeRequest struct {
	ModeId    SessionModeId `json:"modeId"`
	SessionId SessionId     `json:"sessionId"`
}

// Exit status of a terminal command.
type TerminalExitStatus struct {
	ExitCode *int64 `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

// Request to get the current output and status of a terminal.
type TerminalOutputRequest struct {
	SessionId  SessionId `json:"sessionId"`
	TerminalId string    `json:"terminalId"`
}

// Response containing the terminal output and exit status.
type TerminalOutputResponse struct {
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
	Output     string              `json:"output"`
	Truncated  bool                `json:"truncated"`
}

// Text provided to or from an LLM.
type TextContent struct {
	Annotations *Annotations `json:"annotations,omitempty"`
	Text        string       `json:"text"`
}

// Text-based resource contents.
type TextResourceContents struct {
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
	Uri      string `json:"uri"`
}

// Represents a tool call that the language model has requested.
//
// Tool calls are actions that the agent executes on behalf of the language model,
// such as reading files, executing code, or fetching data from external sources.
//
// See protocol docs: [Tool Calls](https://agentclientprotocol.com/protocol/tool-calls)
type ToolCall struct {
	Content    []ToolCallContent  `json:"content,omitempty"`
	Kind       *ToolKind          `json:"kind,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`
	Status     *ToolCallStatus    `json:"status,omitempty"`
	Title      string             `json:"title"`
	ToolCallId ToolCallId         `json:"toolCallId"`
}

// Content produced by a tool call.
//
// Tool calls can produce different types of content including
// standard content blocks (text, images) or file diffs.
//
// See protocol docs: [Content](https://agentclientprotocol.com/protocol/tool-calls#content)
type ToolCallContent struct {
	discriminator string
	content       *ToolCallContentContent
	diff          *ToolCallContentDiff
	terminal      *ToolCallContentTerminal
}

func (t ToolCallContent) MarshalJSON() ([]byte, error) {
	switch t.discriminator {
	case "content":
		return json.Marshal(t.content)
	case "diff":
		return json.Marshal(t.diff)
	case "terminal":
		return json.Marshal(t.terminal)
	}
	return nil, fmt.Errorf("no variant is set for ToolCallContent")
}

func (t *ToolCallContent) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return err
	}

	switch discriminator.Type {
	case "content":
		var v ToolCallContentContent
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		t.content = &v
		t.discriminator = "content"
		return nil
	case "diff":
		var v ToolCallContentDiff
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		t.diff = &v
		t.discriminator = "diff"
		return nil
	case "terminal":
		var v ToolCallContentTerminal
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		t.terminal = &v
		t.discriminator = "terminal"
		return nil
	}
	return fmt.Errorf("unknown discriminator value: %s", discriminator.Type)
}

func (t *ToolCallContent) GetContent() *ToolCallContentContent {
	return t.content
}

func (t *ToolCallContent) IsContent() bool {
	return t.content != nil
}

func (t *ToolCallContent) GetDiff() *ToolCallContentDiff {
	return t.diff
}

func (t *ToolCallContent) IsDiff() bool {
	return t.diff != nil
}

func (t *ToolCallContent) GetTerminal() *ToolCallContentTerminal {
	return t.terminal
}

func (t *ToolCallContent) IsTerminal() bool {
	return t.terminal != nil
}

// Standard content block (text, images, resources).
type ToolCallContentContent struct {
	Content ContentBlock `json:"content"`
	Type    string       `json:"type"`
}

// File modification shown as a diff.
type ToolCallContentDiff struct {
	NewText string `json:"newText"`
	OldText string `json:"oldText,omitempty"`
	Path    string `json:"path"`
	Type    string `json:"type"`
}

// Embed a terminal created with `terminal/create` by its id.
//
// The terminal must be added before calling `terminal/release`.
//
// See protocol docs: [Terminal](https://agentclientprotocol.com/protocol/terminal)
type ToolCallContentTerminal struct {
	TerminalId string `json:"terminalId"`
	Type       string `json:"type"`
}

// A file location being accessed or modified by a tool.
//
// Enables clients to implement "follow-along" features that track
// which files the agent is working with in real-time.
//
// See protocol docs: [Following the Agent](https://agentclientprotocol.com/protocol/tool-calls#following-the-agent)
type ToolCallLocation struct {
	Line *int64 `json:"line,omitempty"`
	Path string `json:"path"`
}

// An update to an existing tool call.
//
// Used to report progress and results as tools execute. All fields except
// the tool call ID are optional - only changed fields need to be included.
//
// See protocol docs: [Updating](https://agentclientprotocol.com/protocol/tool-calls#updating)
type ToolCallUpdate struct {
	Content    []ToolCallContent  `json:"content,omitempty"`
	Kind       *ToolKind          `json:"kind,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   json.RawMessage    `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage    `json:"rawOutput,omitempty"`
	Status     *ToolCallStatus    `json:"status,omitempty"`
	Title      string             `json:"title,omitempty"`
	ToolCallId ToolCallId         `json:"toolCallId"`
}

// Request to wait for a terminal command to exit.
type WaitForTerminalExitRequest struct {
	SessionId  SessionId `json:"sessionId"`
	TerminalId string    `json:"terminalId"`
}

// Response containing the exit status of a terminal command.
type WaitForTerminalExitResponse struct {
	ExitCode *int64 `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

// Request to write content to a text file.
//
// Only available if the client supports the `fs.writeTextFile` capability.
type WriteTextFileRequest struct {
	Content   string    `json:"content"`
	Path      string    `json:"path"`
	SessionId SessionId `json:"sessionId"`
}

// Agent method names
var AgentMethods = struct {
	Authenticate   string
	Initialize     string
	SessionCancel  string
	SessionLoad    string
	SessionNew     string
	SessionPrompt  string
	SessionSetMode string
}{
	Authenticate:   "authenticate",
	Initialize:     "initialize",
	SessionCancel:  "session/cancel",
	SessionLoad:    "session/load",
	SessionNew:     "session/new",
	SessionPrompt:  "session/prompt",
	SessionSetMode: "session/set_mode",
}

// Client method names
var ClientMethods = struct {
	FsReadTextFile           string
	FsWriteTextFile          string
	SessionRequestPermission string
	SessionUpdate            string
	TerminalCreate           string
	TerminalKill             string
	TerminalOutput           string
	TerminalRelease          string
	TerminalWaitForExit      string
}{
	FsReadTextFile:           "fs/read_text_file",
	FsWriteTextFile:          "fs/write_text_file",
	SessionRequestPermission: "session/request_permission",
	SessionUpdate:            "session/update",
	TerminalCreate:           "terminal/create",
	TerminalKill:             "terminal/kill",
	TerminalOutput:           "terminal/output",
	TerminalRelease:          "terminal/release",
	TerminalWaitForExit:      "terminal/wait_for_exit",
}
