package acp

import (
	"context"
	"encoding/json"
)

// TerminalHandle represents a handle to a terminal session.
//
// This handle provides methods to interact with a terminal session
// created via CreateTerminal. It mirrors the TypeScript TerminalHandle
// implementation for consistent API across languages.
//
// The handle supports resource management patterns - always call Release()
// when done with the terminal to free resources.
//
// Note: This is an unstable feature and may be removed or changed.
type TerminalHandle struct {
	ID        string
	sessionID SessionId
	conn      *Connection
}

// NewTerminalHandle creates a new terminal handle.
func NewTerminalHandle(id string, sessionID SessionId, conn *Connection) *TerminalHandle {
	return &TerminalHandle{
		ID:        id,
		sessionID: sessionID,
		conn:      conn,
	}
}

// CurrentOutput gets the current terminal output without waiting for the command to exit.
// This matches the TypeScript TerminalHandle.currentOutput() method.
func (t *TerminalHandle) CurrentOutput(ctx context.Context) (*TerminalOutputResponse, error) {
	params := &TerminalOutputRequest{
		SessionId:  t.sessionID,
		TerminalId: t.ID,
	}
	
	data, err := t.conn.SendRequest(ctx, ClientMethods.TerminalOutput, params)
	if err != nil {
		return nil, err
	}
	
	var response TerminalOutputResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// WaitForExit waits for the terminal command to complete and returns its exit status.
// This matches the TypeScript TerminalHandle.waitForExit() method.
func (t *TerminalHandle) WaitForExit(ctx context.Context) (*WaitForTerminalExitResponse, error) {
	params := &WaitForTerminalExitRequest{
		SessionId:  t.sessionID,
		TerminalId: t.ID,
	}
	
	data, err := t.conn.SendRequest(ctx, ClientMethods.TerminalWaitForExit, params)
	if err != nil {
		return nil, err
	}
	
	var response WaitForTerminalExitResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// Kill kills the terminal command without releasing the terminal.
//
// The terminal remains valid after killing, allowing you to:
// - Get the final output with CurrentOutput()
// - Check the exit status
// - Release the terminal when done
//
// Useful for implementing timeouts or cancellation.
// This matches the TypeScript TerminalHandle.kill() method.
func (t *TerminalHandle) Kill(ctx context.Context) error {
	params := &KillTerminalCommandRequest{
		SessionId:  t.sessionID,
		TerminalId: t.ID,
	}
	
	_, err := t.conn.SendRequest(ctx, ClientMethods.TerminalKill, params)
	return err
}

// Release releases the terminal and frees all associated resources.
//
// If the command is still running, it will be killed.
// After release, the terminal ID becomes invalid and cannot be used
// with other terminal methods.
//
// Tool calls that already reference this terminal will continue to
// display its output.
//
// **Important:** Always call this method when done with the terminal.
// This matches the TypeScript TerminalHandle.release() method.
func (t *TerminalHandle) Release(ctx context.Context) error {
	params := &ReleaseTerminalRequest{
		SessionId:  t.sessionID,
		TerminalId: t.ID,
	}
	
	_, err := t.conn.SendRequest(ctx, ClientMethods.TerminalRelease, params)
	return err
}