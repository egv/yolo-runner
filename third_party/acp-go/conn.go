package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultRequestTimeout controls how long SendRequest waits for a response
// when the provided context does not have a deadline.
//
// Set to 0 to disable timeouts and rely solely on context cancellation.
var DefaultRequestTimeout time.Duration

// Connection represents a bidirectional JSON-RPC connection
type Connection struct {
	reader           io.Reader
	writer           io.Writer
	pendingResponses sync.Map // map[int64]*pendingResponse
	nextRequestID    int64
	handler          MethodHandler
	mu               sync.RWMutex
	writeQueue       chan jsonRpcMessage
	ctx              context.Context
	cancel           context.CancelFunc
}

// MethodHandler handles incoming JSON-RPC method calls
type MethodHandler func(method string, params json.RawMessage) (any, error)

// pendingResponse represents a response waiting for completion
type pendingResponse struct {
	result chan responseResult
	cancel context.CancelFunc
}

// responseResult contains the result or error from a JSON-RPC call
type responseResult struct {
	data  json.RawMessage
	error error
}

// jsonRpcMessage represents a JSON-RPC message
type jsonRpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRpcError   `json:"error,omitempty"`
}

// jsonRpcError represents a JSON-RPC error
type jsonRpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// NewConnection creates a new bidirectional JSON-RPC connection
func NewConnection(handler MethodHandler, reader io.Reader, writer io.Writer) *Connection {
	ctx, cancel := context.WithCancel(context.Background())

	conn := &Connection{
		reader:     reader,
		writer:     writer,
		handler:    handler,
		writeQueue: make(chan jsonRpcMessage, 100),
		ctx:        ctx,
		cancel:     cancel,
	}

	return conn
}

// Start begins processing JSON-RPC messages
func (c *Connection) Start(ctx context.Context) error {
	// Start writer goroutine
	go c.writeLoop()

	// Start reader loop (blocks until done)
	return c.readLoop(ctx)
}

// Close closes the connection
func (c *Connection) Close() error {
	c.cancel()
	close(c.writeQueue)
	return nil
}

// readLoop reads and processes incoming messages
func (c *Connection) readLoop(ctx context.Context) error {
	scanner := bufio.NewScanner(c.reader)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		go c.processMessage(line)
	}

	return scanner.Err()
}

// writeLoop writes outgoing messages
func (c *Connection) writeLoop() {
	for {
		select {
		case msg, ok := <-c.writeQueue:
			if !ok {
				return // Channel closed
			}

			data, err := json.Marshal(msg)
			if err != nil {
				continue // Skip malformed messages
			}

			data = append(data, '\n')
			c.writer.Write(data)

		case <-c.ctx.Done():
			return
		}
	}
}

// processMessage processes a single JSON-RPC message
func (c *Connection) processMessage(line string) {
	var msg jsonRpcMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return // Skip malformed messages
	}

	if msg.ID != nil && msg.Method != "" {
		// It's a request
		c.handleRequest(msg)
	} else if msg.Method != "" {
		// It's a notification
		c.handleNotification(msg)
	} else if msg.ID != nil {
		// It's a response
		c.handleResponse(msg)
	}
}

// handleRequest processes incoming requests
func (c *Connection) handleRequest(msg jsonRpcMessage) {
	response := jsonRpcMessage{
		Jsonrpc: "2.0",
		ID:      msg.ID,
	}

	if c.handler != nil {
		result, err := c.handler(msg.Method, msg.Params)
		if err != nil {
			response.Error = &jsonRpcError{
				Code:    -32603,
				Message: err.Error(),
			}
		} else if result != nil {
			if data, marshalErr := json.Marshal(result); marshalErr == nil {
				response.Result = data
			} else {
				response.Error = &jsonRpcError{
					Code:    -32603,
					Message: "Failed to marshal result",
				}
			}
		}
	} else {
		response.Error = &jsonRpcError{
			Code:    -32601,
			Message: "Method not found",
		}
	}

	// Send response
	select {
	case c.writeQueue <- response:
	case <-c.ctx.Done():
	}
}

// handleNotification processes incoming notifications
func (c *Connection) handleNotification(msg jsonRpcMessage) {
	if c.handler != nil {
		c.handler(msg.Method, msg.Params) // Ignore result for notifications
	}
}

// handleResponse processes incoming responses
func (c *Connection) handleResponse(msg jsonRpcMessage) {
	if msg.ID == nil {
		return
	}

	if pending, ok := c.pendingResponses.Load(*msg.ID); ok {
		p := pending.(*pendingResponse)

		var result responseResult
		if msg.Error != nil {
			result.error = fmt.Errorf("JSON-RPC error %d: %s", msg.Error.Code, msg.Error.Message)
		} else {
			result.data = msg.Result
		}

		select {
		case p.result <- result:
		default:
			// Channel might be closed or blocked
		}
	}
}

// SendRequest sends a JSON-RPC request and waits for the response
func (c *Connection) SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Generate unique request ID
	requestID := atomic.AddInt64(&c.nextRequestID, 1)

	// Create response channel
	pending := &pendingResponse{
		result: make(chan responseResult, 1),
	}

	// Create cancellable context
	reqCtx, cancel := context.WithCancel(ctx)
	pending.cancel = cancel

	// Store pending response
	c.pendingResponses.Store(requestID, pending)

	// Cleanup on exit
	defer func() {
		c.pendingResponses.Delete(requestID)
		cancel()
		close(pending.result)
	}()

	// Prepare the request message
	msg := jsonRpcMessage{
		Jsonrpc: "2.0",
		ID:      &requestID,
		Method:  method,
	}

	if params != nil {
		if data, err := json.Marshal(params); err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		} else {
			msg.Params = data
		}
	}

	// Send the request
	select {
	case c.writeQueue <- msg:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}

	// Wait for response.
	timeout := DefaultRequestTimeout
	if deadline, ok := ctx.Deadline(); ok {
		until := time.Until(deadline)
		if until > 0 && (timeout == 0 || until < timeout) {
			timeout = until
		}
	}
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}
	select {
	case result := <-pending.result:
		if result.error != nil {
			return nil, result.error
		}
		return result.data, nil
	case <-timeoutCh:
		return nil, fmt.Errorf("request %s timed out after %s", method, timeout)
	case <-reqCtx.Done():
		return nil, reqCtx.Err()
	}
}

// SendNotification sends a JSON-RPC notification (no response expected)
func (c *Connection) SendNotification(ctx context.Context, method string, params any) error {
	msg := jsonRpcMessage{
		Jsonrpc: "2.0",
		Method:  method,
	}

	if params != nil {
		if data, err := json.Marshal(params); err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		} else {
			msg.Params = data
		}
	}

	select {
	case c.writeQueue <- msg:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}
