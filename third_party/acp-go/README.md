![agent client protocol golang banner](./docs/imgs/banner-dark.jpg)

**🇺🇸 English** | [🇰🇷 한국어](./docs/README.ko.md)

# Agent Client Protocol - Go Implementation

A Go implementation of the Agent Client Protocol (ACP), which standardizes communication between _code editors_ (interactive programs for viewing and editing source code) and _coding agents_ (programs that use generative AI to autonomously modify code).

This is an **unofficial** implementation of the ACP specification in Go. The official protocol specification and reference implementations can be found at the [official repository](https://github.com/zed-industries/agent-client-protocol).

> [!NOTE]
> The Agent Client Protocol is under active development. This implementation may lag behind the latest specification changes. Please refer to the [official repository](https://github.com/zed-industries/agent-client-protocol) for the most up-to-date protocol specification.

Learn more about the protocol at [agentclientprotocol.com](https://agentclientprotocol.com/).

## Installation

```bash
go get github.com/ironpark/go-acp
```

## Example Code
See the [docs/example](./docs/example/) directory for complete working examples:

- **[Agent Example](./docs/example/agent/)** - Comprehensive agent implementation demonstrating session management, tool calls, and permission requests
- **[Client Example](./docs/example/client/)** - Client implementation that spawns the agent and communicates with it

## Architecture

This implementation provides a clean, modern architecture with bidirectional JSON-RPC 2.0 communication:

- **`Connection`**: Unified bidirectional transport layer handling stdin/stdout communication with concurrent request/response correlation
- **`AgentSideConnection`**: High-level ACP interface for implementing agents, wraps Connection for agent-specific operations
- **`ClientSideConnection`**: High-level ACP interface for implementing clients, wraps Connection for client-specific operations
- **`TerminalHandle`**: Resource management wrapper for terminal sessions with automatic cleanup patterns
- **Generated Types**: Complete type-safe Go structs generated from the official ACP JSON schema

## Protocol Support

This implementation supports ACP Protocol Version 1 with the following features:

### Agent Methods (Client → Agent)
- `initialize` - Initialize the agent and negotiate capabilities
- `authenticate` - Authenticate with the agent (optional)
- `session/new` - Create a new conversation session
- `session/load` - Load an existing session (if supported)
- `session/set_mode` - Change session mode (unstable)
- `session/prompt` - Send user prompt to agent
- `session/cancel` - Cancel ongoing operations

### Client Methods (Agent → Client)
- `session/update` - Send session updates (notifications)
- `session/request_permission` - Request user permission for operations
- `fs/read_text_file` - Read text file from client filesystem
- `fs/write_text_file` - Write text file to client filesystem
- **Terminal Support** (unstable):
  - `terminal/create` - Create terminal session
  - `terminal/output` - Get terminal output
  - `terminal/wait_for_exit` - Wait for terminal exit
  - `terminal/kill` - Kill terminal process
  - `terminal/release` - Release terminal handle


## Contributing

This is an unofficial implementation. For protocol specification changes, please contribute to the [official repository](https://github.com/zed-industries/agent-client-protocol).

For Go implementation issues and improvements, please open an issue or pull request.

## Related Projects

- **Official ACP Repository**: [zed-industries/agent-client-protocol](https://github.com/zed-industries/agent-client-protocol)
- **Rust Implementation**: Part of the official repository
- **Protocol Documentation**: [agentclientprotocol.com](https://agentclientprotocol.com/)

### Editors with ACP Support

- [Zed](https://zed.dev/docs/ai/external-agents)
- [neovim](https://neovim.io) through the [CodeCompanion](https://github.com/olimorris/codecompanion.nvim) plugin
- [yetone/avante.nvim](https://github.com/yetone/avante.nvim): A Neovim plugin designed to emulate the behaviour of the Cursor AI IDE