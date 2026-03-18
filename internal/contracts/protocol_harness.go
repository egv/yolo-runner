package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  map[string]any  `json:"params,omitempty"`
	Result  map[string]any  `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type FakeStdioJSONRPCHarness struct {
	clientWriter io.WriteCloser
	clientReader io.ReadCloser
	serverWriter io.WriteCloser
	serverReader io.ReadCloser
	serverStream *harnessByteStream

	readMu  sync.Mutex
	writeMu sync.Mutex
}

func NewFakeStdioJSONRPCHarness() *FakeStdioJSONRPCHarness {
	clientToServer := newHarnessByteStream()
	serverToClient := newHarnessByteStream()
	serverReader := clientToServer.Reader()
	return &FakeStdioJSONRPCHarness{
		clientWriter: clientToServer.Writer(),
		clientReader: serverToClient.Reader(),
		serverWriter: serverToClient.Writer(),
		serverReader: serverReader,
		serverStream: clientToServer,
	}
}

func (h *FakeStdioJSONRPCHarness) ClientIO() (io.WriteCloser, io.ReadCloser) {
	if h == nil {
		return nil, nil
	}
	return h.clientWriter, h.clientReader
}

func (h *FakeStdioJSONRPCHarness) ServerIO() (io.WriteCloser, io.ReadCloser) {
	if h == nil {
		return nil, nil
	}
	return h.serverWriter, h.serverReader
}

func (h *FakeStdioJSONRPCHarness) ReadMessage(ctx context.Context) (JSONRPCMessage, error) {
	if h == nil {
		return JSONRPCMessage{}, errors.New("stdio json-rpc harness is nil")
	}
	h.readMu.Lock()
	defer h.readMu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	payload, err := h.readJSONRPCPayload(ctx)
	if err != nil {
		return JSONRPCMessage{}, err
	}
	var msg JSONRPCMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return JSONRPCMessage{}, err
	}
	return msg, nil
}

func (h *FakeStdioJSONRPCHarness) SendMessage(msg JSONRPCMessage) error {
	if h == nil {
		return errors.New("stdio json-rpc harness is nil")
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return json.NewEncoder(h.serverWriter).Encode(msg)
}

func (h *FakeStdioJSONRPCHarness) Close() error {
	if h == nil {
		return nil
	}
	var errs []error
	for _, closeFn := range []func() error{
		h.clientWriter.Close,
		h.clientReader.Close,
		h.serverWriter.Close,
		h.serverReader.Close,
	} {
		if err := closeFn(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *FakeStdioJSONRPCHarness) readJSONRPCPayload(ctx context.Context) ([]byte, error) {
	if h.serverStream == nil {
		return nil, io.EOF
	}

	var payload bytes.Buffer
	depth := 0
	inString := false
	escaped := false
	started := false

	for {
		b, err := h.serverStream.readByteContext(ctx)
		if err != nil {
			return nil, err
		}

		if !started {
			if isJSONWhitespace(b) {
				continue
			}
			if b != '{' && b != '[' {
				return nil, fmt.Errorf("unexpected json-rpc payload prefix %q", b)
			}
			started = true
		}

		payload.WriteByte(b)

		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return payload.Bytes(), nil
			}
		}
	}
}

func isJSONWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

type harnessByteStream struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	closed bool
	notify chan struct{}
}

func newHarnessByteStream() *harnessByteStream {
	return &harnessByteStream{
		notify: make(chan struct{}),
	}
}

func (s *harnessByteStream) Reader() io.ReadCloser {
	return harnessReadCloser{stream: s}
}

func (s *harnessByteStream) Writer() io.WriteCloser {
	return harnessWriteCloser{stream: s}
}

func (s *harnessByteStream) read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if s.buffer.Len() > 0 {
			n, err := s.buffer.Read(p)
			s.mu.Unlock()
			return n, err
		}
		if s.closed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		notify := s.notify
		s.mu.Unlock()
		<-notify
	}
}

func (s *harnessByteStream) write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, io.ErrClosedPipe
	}
	n, err := s.buffer.Write(p)
	s.signalLocked()
	return n, err
}

func (s *harnessByteStream) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	s.signalLocked()
	return nil
}

func (s *harnessByteStream) readByteContext(ctx context.Context) (byte, error) {
	for {
		s.mu.Lock()
		if s.buffer.Len() > 0 {
			b, err := s.buffer.ReadByte()
			s.mu.Unlock()
			return b, err
		}
		if s.closed {
			s.mu.Unlock()
			return 0, io.EOF
		}
		notify := s.notify
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-notify:
		}
	}
}

func (s *harnessByteStream) signalLocked() {
	close(s.notify)
	s.notify = make(chan struct{})
}

type harnessReadCloser struct {
	stream *harnessByteStream
}

func (r harnessReadCloser) Read(p []byte) (int, error) {
	if r.stream == nil {
		return 0, io.EOF
	}
	return r.stream.read(p)
}

func (r harnessReadCloser) Close() error {
	if r.stream == nil {
		return nil
	}
	return r.stream.close()
}

type harnessWriteCloser struct {
	stream *harnessByteStream
}

func (w harnessWriteCloser) Write(p []byte) (int, error) {
	if w.stream == nil {
		return 0, io.ErrClosedPipe
	}
	return w.stream.write(p)
}

func (w harnessWriteCloser) Close() error {
	if w.stream == nil {
		return nil
	}
	return w.stream.close()
}

type SSEEvent struct {
	Event string
	Data  string
	ID    string
}

type RecordedHTTPRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type queuedHTTPResponse struct {
	status int
	body   []byte
}

type FakeHTTPSSEHarness struct {
	server *httptest.Server

	mu           sync.Mutex
	healthStatus int
	requests     map[string][]RecordedHTTPRequest
	responses    map[string][]queuedHTTPResponse
	sseClients   map[chan string]struct{}
	pendingSSE   []string
}

func NewFakeHTTPSSEHarness() *FakeHTTPSSEHarness {
	h := &FakeHTTPSSEHarness{
		healthStatus: http.StatusOK,
		requests:     map[string][]RecordedHTTPRequest{},
		responses:    map[string][]queuedHTTPResponse{},
		sseClients:   map[chan string]struct{}{},
	}
	h.server = httptest.NewServer(http.HandlerFunc(h.serveHTTP))
	return h
}

func (h *FakeHTTPSSEHarness) Client() *http.Client {
	if h == nil || h.server == nil {
		return http.DefaultClient
	}
	return h.server.Client()
}

func (h *FakeHTTPSSEHarness) URL(path string) string {
	if h == nil || h.server == nil {
		return ""
	}
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return h.server.URL + path
}

func (h *FakeHTTPSSEHarness) HealthURL() string {
	return h.URL("/health")
}

func (h *FakeHTTPSSEHarness) SSEURL() string {
	return h.URL("/events")
}

func (h *FakeHTTPSSEHarness) SetHealthStatus(status int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.healthStatus = status
}

func (h *FakeHTTPSSEHarness) QueueJSONResponse(path string, status int, body any) {
	if h == nil {
		return
	}
	payload, err := json.Marshal(body)
	if err != nil {
		payload = []byte(`{"error":"marshal failed"}`)
		status = http.StatusInternalServerError
	}
	path = normalizeHarnessPath(path)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.responses[path] = append(h.responses[path], queuedHTTPResponse{status: status, body: payload})
}

func (h *FakeHTTPSSEHarness) Requests(path string) []RecordedHTTPRequest {
	if h == nil {
		return nil
	}
	path = normalizeHarnessPath(path)
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]RecordedHTTPRequest(nil), h.requests[path]...)
	for i := range out {
		out[i].Header = out[i].Header.Clone()
		out[i].Body = append([]byte(nil), out[i].Body...)
	}
	return out
}

func (h *FakeHTTPSSEHarness) SendSSE(event SSEEvent) error {
	if h == nil {
		return errors.New("http/sse harness is nil")
	}
	payload := formatSSEEvent(event)

	h.mu.Lock()
	if len(h.sseClients) == 0 {
		h.pendingSSE = append(h.pendingSSE, payload)
		h.mu.Unlock()
		return nil
	}
	clients := make([]chan string, 0, len(h.sseClients))
	for client := range h.sseClients {
		clients = append(clients, client)
	}
	h.mu.Unlock()

	var blocked []chan string
	for _, client := range clients {
		select {
		case client <- payload:
		default:
			blocked = append(blocked, client)
		}
	}

	if len(blocked) == 0 {
		return nil
	}

	h.mu.Lock()
	for _, client := range blocked {
		delete(h.sseClients, client)
	}
	h.mu.Unlock()
	return nil
}

func (h *FakeHTTPSSEHarness) Close() {
	if h == nil {
		return
	}
	if h.server != nil {
		h.server.Close()
	}
}

func (h *FakeHTTPSSEHarness) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch normalizeHarnessPath(r.URL.Path) {
	case "/health":
		h.mu.Lock()
		status := h.healthStatus
		h.mu.Unlock()
		w.WriteHeader(status)
		return
	case "/events":
		h.serveSSE(w, r)
		return
	default:
		h.serveQueuedHTTP(w, r)
		return
	}
}

func (h *FakeHTTPSSEHarness) serveQueuedHTTP(w http.ResponseWriter, r *http.Request) {
	path := normalizeHarnessPath(r.URL.Path)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusInternalServerError)
		return
	}
	_ = r.Body.Close()

	h.mu.Lock()
	h.requests[path] = append(h.requests[path], RecordedHTTPRequest{
		Method: r.Method,
		Path:   path,
		Header: r.Header.Clone(),
		Body:   append([]byte(nil), body...),
	})
	queue := h.responses[path]
	if len(queue) == 0 {
		h.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	response := queue[0]
	h.responses[path] = queue[1:]
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body)
}

func (h *FakeHTTPSSEHarness) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	client := make(chan string, 8)

	h.mu.Lock()
	pending := append([]string(nil), h.pendingSSE...)
	h.pendingSSE = nil
	h.sseClients[client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.sseClients, client)
		close(client)
		h.mu.Unlock()
	}()

	for _, payload := range pending {
		if _, err := io.WriteString(w, payload); err != nil {
			return
		}
		flusher.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-client:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func normalizeHarnessPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func formatSSEEvent(event SSEEvent) string {
	var builder strings.Builder
	if id := strings.TrimSpace(event.ID); id != "" {
		builder.WriteString("id: ")
		builder.WriteString(id)
		builder.WriteString("\n")
	}
	if name := strings.TrimSpace(event.Event); name != "" {
		builder.WriteString("event: ")
		builder.WriteString(name)
		builder.WriteString("\n")
	}
	for _, line := range strings.Split(strings.ReplaceAll(event.Data, "\r\n", "\n"), "\n") {
		builder.WriteString("data: ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	return builder.String()
}
