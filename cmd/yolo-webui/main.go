package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/ui/monitor"
	"github.com/egv/yolo-runner/v2/internal/version"
	"golang.org/x/net/websocket"
)

//go:embed webapp/index.html
var webUIIndexHTML string

//go:embed webapp/app.js
var webUIAppJS string

const (
	defaultBusBackend = "redis"
	defaultBusPrefix  = "yolo"
)

var newDistributedBus = func(backend string, address string, opts distributed.BusBackendOptions) (distributed.Bus, error) {
	switch strings.TrimSpace(backend) {
	case "redis":
		return distributed.NewRedisBus(address, opts)
	case "nats":
		return distributed.NewNATSBus(address, opts)
	default:
		return nil, fmt.Errorf("unsupported distributed bus backend %q", backend)
	}
}

type runConfig struct {
	repoRoot            string
	listenAddr          string
	authToken           string
	busBackend          string
	busAddress          string
	busPrefix           string
	busSource           string
	busOptions          distributed.BusBackendOptions
	taskStatusAuthToken string
	taskStatusBackends  []string
	shutdownTimeout     time.Duration
}

type uiConfig struct {
	Source string `json:"source"`
}

type controlRequest struct {
	Action          string            `json:"action"`
	Source          string            `json:"source"`
	TaskID          string            `json:"task_id"`
	Status          string            `json:"status"`
	Comment         string            `json:"comment"`
	CommandID       string            `json:"command_id"`
	ExpectedVersion int64             `json:"expected_version"`
	Backends        []string          `json:"backends"`
	Metadata        map[string]string `json:"metadata"`
	StatusAuthToken string            `json:"status_auth_token"`
}

type controlResponse struct {
	Status    string `json:"status"`
	CommandID string `json:"command_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type statePayload struct {
	State  monitor.UIState `json:"state"`
	Config uiConfig        `json:"config"`
}

type webuiState struct {
	monitor   *monitor.Model
	monitorMu sync.RWMutex
	config    uiConfig
	configMu  sync.RWMutex
	hub       *stateBroadcaster
	authToken string
	bus       distributed.Bus
	subjects  distributed.EventSubjects

	taskStatusAuthToken string
	taskStatusBackends  []string
}

type stateBroadcaster struct {
	mu          sync.Mutex
	subscribers map[chan statePayload]struct{}
}

var errStatusAuth = errors.New("invalid status_auth_token")

func main() {
	os.Exit(RunMain(os.Args[1:], nil))
}

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	if version.IsVersionRequest(args) {
		version.Print(os.Stdout, "yolo-webui")
		return 0
	}

	fs := flag.NewFlagSet("yolo-webui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoRoot := fs.String("repo", ".", "Repository root")
	listen := fs.String("listen", ":8080", "HTTP listen address")
	authToken := fs.String("auth-token", "", "Bearer token required for /api and /ws requests (empty disables auth)")
	busBackend := fs.String("distributed-bus-backend", "", "Distributed bus backend (redis, nats)")
	busAddress := fs.String("distributed-bus-address", "", "Distributed bus address")
	busPrefix := fs.String("distributed-bus-prefix", "", "Distributed bus subject prefix")
	busSource := fs.String("events-bus-source", "", "Monitor source filter")
	taskStatusAuthToken := fs.String("task-status-auth-token", "", "Token required to publish task status updates through mastermind")
	taskStatusBackends := fs.String("task-status-backends", "", "Comma-separated task-status update backends (defaults to all)")
	shutdownTimeout := fs.Duration("shutdown-timeout", 5*time.Second, "Graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	selectedBusConfig, err := resolveWebUIDistributedBusConfig(
		*repoRoot,
		*busBackend,
		*busAddress,
		*busPrefix,
		*busSource,
		os.Getenv,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *shutdownTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "--shutdown-timeout must be greater than 0")
		return 1
	}
	listenAddr := strings.TrimSpace(*listen)
	if listenAddr == "" {
		fmt.Fprintln(os.Stderr, "--listen is required")
		return 1
	}

	if run == nil {
		run = defaultRun
	}

	cfg := runConfig{
		repoRoot:            strings.TrimSpace(*repoRoot),
		listenAddr:          listenAddr,
		authToken:           strings.TrimSpace(*authToken),
		busBackend:          selectedBusConfig.Backend,
		busAddress:          selectedBusConfig.Address,
		busPrefix:           selectedBusConfig.Prefix,
		busSource:           selectedBusConfig.Source,
		busOptions:          selectedBusConfig.BackendOptions(),
		taskStatusAuthToken: strings.TrimSpace(*taskStatusAuthToken),
		taskStatusBackends:  parseCommaSeparatedValues(*taskStatusBackends),
		shutdownTimeout:     *shutdownTimeout,
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	bus, err := newDistributedBus(cfg.busBackend, cfg.busAddress, cfg.busOptions)
	if err != nil {
		return err
	}
	defer func() {
		_ = bus.Close()
	}()

	runtime := newWebUIState(cfg.authToken, cfg.busSource, bus)
	runtime.subjects = distributed.DefaultEventSubjects(cfg.busPrefix)
	runtime.taskStatusAuthToken = cfg.taskStatusAuthToken
	runtime.taskStatusBackends = cfg.taskStatusBackends
	defer runtime.hub.shutdown()
	if cfg.busSource == "" {
		runtime.setConfig(uiConfig{Source: ""})
	}
	go runtime.consumeMonitorEvents(ctx, cfg.busPrefix)

	mux := http.NewServeMux()
	mux.HandleFunc("/", runtime.handleIndex)
	mux.HandleFunc("/app.js", runtime.handleAppJS)
	mux.HandleFunc("/api/state", runtime.handleAPIState)
	mux.HandleFunc("/api/config", runtime.handleAPIConfig)
	mux.HandleFunc("/api/control", runtime.handleAPIControl)
	mux.HandleFunc("/ws", runtime.handleWS)

	server := &http.Server{Addr: cfg.listenAddr, Handler: mux}
	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-shutdownCtx.Done()
		serverCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		_ = server.Shutdown(serverCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func newWebUIState(authToken string, source string, bus distributed.Bus) *webuiState {
	return &webuiState{
		monitor:   monitor.NewModel(nil),
		config:    uiConfig{Source: source},
		hub:       newStateBroadcaster(),
		authToken: authToken,
		bus:       bus,
		subjects:  distributed.DefaultEventSubjects(defaultBusPrefix),
	}
}

func (state *webuiState) currentConfig() uiConfig {
	state.configMu.Lock()
	defer state.configMu.Unlock()
	return state.config
}

func (state *webuiState) currentState() monitor.UIState {
	state.monitorMu.Lock()
	defer state.monitorMu.Unlock()
	return state.monitor.UIState()
}

func (state *webuiState) snapshot() statePayload {
	return statePayload{
		State:  state.currentState(),
		Config: state.currentConfig(),
	}
}

func (state *webuiState) setConfig(next uiConfig) {
	state.configMu.Lock()
	state.config = next
	state.configMu.Unlock()
	state.hub.broadcast(state.snapshot())
}

func (state *webuiState) hasAuth(r *http.Request) bool {
	if state.authToken == "" {
		return true
	}
	expected := "Bearer " + state.authToken
	if strings.TrimSpace(r.URL.Query().Get("token")) == state.authToken {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Authorization")), expected)
}

func (state *webuiState) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if state.hasAuth(r) {
		return true
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, "unauthorized")
	return false
}

func (state *webuiState) handleIndex(w http.ResponseWriter, r *http.Request) {
	if !state.requireAuth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(webUIIndexHTML))
}

func (state *webuiState) handleAppJS(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(webUIAppJS))
}

func (state *webuiState) handleAPIState(w http.ResponseWriter, r *http.Request) {
	if !state.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, statePayload{State: state.currentState(), Config: state.currentConfig()})
}

func (state *webuiState) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	if !state.requireAuth(w, r) {
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, state.currentConfig())
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var cfg uiConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	state.setConfig(uiConfig{Source: strings.TrimSpace(cfg.Source)})
	writeJSON(w, http.StatusOK, state.snapshot())
}

func (state *webuiState) handleAPIControl(w http.ResponseWriter, r *http.Request) {
	if !state.requireAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	request := controlRequest{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch strings.ToLower(strings.TrimSpace(request.Action)) {
	case "set-source":
		state.setConfig(uiConfig{Source: strings.TrimSpace(request.Source)})
		writeJSON(w, http.StatusOK, controlResponse{Status: "ok"})
	case "set-task-status":
		commandID, err := state.publishTaskStatusUpdate(r.Context(), request)
		if err != nil {
			if errors.Is(err, errStatusAuth) {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(controlResponse{
					Status: "error",
					Error:  err.Error(),
				})
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(controlResponse{
				Status: "error",
				Error:  err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusAccepted, controlResponse{
			Status:    "accepted",
			CommandID: commandID,
		})
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(controlResponse{Status: "error", Error: "unsupported action"})
	}
}

func (state *webuiState) handleWS(w http.ResponseWriter, r *http.Request) {
	if !state.requireAuth(w, r) {
		return
	}
	websocket.Handler(func(rawConn *websocket.Conn) {
		state.serveWS(rawConn)
	}).ServeHTTP(w, r)
}

func (state *webuiState) serveWS(rawConn *websocket.Conn) {
	r := rawConn.Request()
	if r == nil {
		_ = rawConn.Close()
		return
	}
	updates := state.hub.register()
	defer state.hub.unregister(updates)
	_ = websocket.JSON.Send(rawConn, state.snapshot())
	for msg := range updates {
		if err := websocket.JSON.Send(rawConn, msg); err != nil {
			return
		}
	}
}

func (state *webuiState) consumeMonitorEvents(ctx context.Context, busPrefix string) {
	subject := distributed.DefaultEventSubjects(busPrefix).MonitorEvent
	rawEvents, unsubscribe, err := state.bus.Subscribe(ctx, subject)
	if err != nil {
		return
	}
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-rawEvents:
			if !ok {
				return
			}
			filter := state.currentConfig().Source
			event, shouldUse, parseErr := parseMonitorEnvelope(env, filter)
			if parseErr != nil {
				continue
			}
			if !shouldUse {
				continue
			}
			state.monitorMu.Lock()
			state.monitor.Apply(event)
			state.monitorMu.Unlock()
			state.hub.broadcast(state.snapshot())
		}
	}
}

func parseMonitorEnvelope(envelope distributed.EventEnvelope, sourceFilter string) (contracts.Event, bool, error) {
	if envelope.Type != distributed.EventTypeMonitorEvent {
		return contracts.Event{}, false, nil
	}
	if sourceFilter != "" && strings.TrimSpace(envelope.Source) != strings.TrimSpace(sourceFilter) {
		return contracts.Event{}, false, nil
	}
	payload := distributed.MonitorEventPayload{}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return contracts.Event{}, false, err
	}
	return payload.Event, true, nil
}

func (state *webuiState) publishTaskStatusUpdate(ctx context.Context, request controlRequest) (string, error) {
	taskID := strings.TrimSpace(request.TaskID)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	status, ok := parseTaskStatus(strings.TrimSpace(request.Status))
	if !ok {
		return "", fmt.Errorf("unsupported task status %q", request.Status)
	}
	commandID := strings.TrimSpace(request.CommandID)
	if commandID == "" {
		commandID = fmt.Sprintf("webui-%d", time.Now().UnixNano())
	}

	authToken := strings.TrimSpace(request.StatusAuthToken)
	expected := strings.TrimSpace(state.taskStatusAuthToken)
	if expected != "" {
		if authToken != expected {
			return "", errStatusAuth
		}
		authToken = expected
	} else {
		authToken = ""
	}
	if expected == "" && strings.TrimSpace(request.StatusAuthToken) != "" {
		authToken = ""
	}
	if expected != "" && authToken == "" {
		return "", errStatusAuth
	}
	normalizedBackends := normalizeStringList(request.Backends, state.taskStatusBackends)

	payload := distributed.TaskStatusUpdatePayload{
		CommandID:       commandID,
		Backends:        normalizedBackends,
		TaskID:          taskID,
		Status:          status,
		Comment:         strings.TrimSpace(request.Comment),
		Metadata:        sanitizeMetadata(request.Metadata),
		ExpectedVersion: request.ExpectedVersion,
		AuthToken:       authToken,
	}
	envelope, err := distributed.NewEventEnvelope(distributed.EventTypeTaskStatusUpdate, "webui", payload.CommandID, payload)
	if err != nil {
		return "", err
	}
	if err := state.bus.Publish(ctx, state.subjects.TaskStatusUpdate, envelope); err != nil {
		return "", err
	}
	return payload.CommandID, nil
}

func parseTaskStatus(raw string) (contracts.TaskStatus, bool) {
	status := contracts.TaskStatus(strings.ToLower(strings.TrimSpace(raw)))
	switch status {
	case contracts.TaskStatusOpen, contracts.TaskStatusInProgress, contracts.TaskStatusBlocked, contracts.TaskStatusClosed, contracts.TaskStatusFailed:
		return status, true
	default:
		return "", false
	}
}

func parseCommaSeparatedValues(raw string) []string {
	return normalizeStringList(strings.Split(raw, ","), nil)
}

func normalizeStringList(values []string, fallback []string) []string {
	out := make([]string, 0, len(values)+len(fallback))
	seen := map[string]struct{}{}
	for _, value := range values {
		next := strings.ToLower(strings.TrimSpace(value))
		if next == "" {
			continue
		}
		if _, exists := seen[next]; exists {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	for _, value := range fallback {
		next := strings.ToLower(strings.TrimSpace(value))
		if next == "" {
			continue
		}
		if _, exists := seen[next]; exists {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	sort.Strings(out)
	return out
}

func sanitizeMetadata(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range raw {
		cleanKey := strings.TrimSpace(key)
		cleanValue := strings.TrimSpace(value)
		if cleanKey == "" {
			continue
		}
		out[cleanKey] = cleanValue
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func newStateBroadcaster() *stateBroadcaster {
	return &stateBroadcaster{
		subscribers: map[chan statePayload]struct{}{},
	}
}

func (b *stateBroadcaster) register() chan statePayload {
	ch := make(chan statePayload, 32)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *stateBroadcaster) unregister(ch chan statePayload) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscribers, ch)
	close(ch)
}

func (b *stateBroadcaster) shutdown() {
	b.mu.Lock()
	for ch := range b.subscribers {
		close(ch)
	}
	b.subscribers = map[chan statePayload]struct{}{}
	b.mu.Unlock()
}

func (b *stateBroadcaster) broadcast(msg statePayload) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func legacyRenderWebUIPage() string {
	return `<!DOCTYPE html>
<html>
  <head>
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>yolo-webui</title>
    <style>
      :root {
        --bg: #0b1020;
        --panel: #141f3a;
        --muted: #9aa4c4;
        --text: #e6ecff;
        --accent: #7cc8ff;
        --accent2: #8de6a7;
      }
      body { margin: 0; background: radial-gradient(circle at 20% 0%, #102347, #05070f 42%), linear-gradient(140deg, #090d18, #0e1630); color: var(--text); font-family: "Georgia", "Times New Roman", serif; }
      main { max-width: 1280px; margin: 0 auto; padding: 1rem; display: grid; gap: 1rem; }
      section { background: color-mix(in srgb, var(--panel) 85%, #000 15%); border: 1px solid #2a365d; border-radius: 12px; padding: 1rem; }
      h1 { margin: 0; font-size: 1.2rem; }
      h2 { margin-top: 0; font-size: 1rem; }
      pre { white-space: pre-wrap; overflow: auto; max-height: 16rem; background: #080f1e; border-radius: 8px; border: 1px solid #2c3a62; padding: 0.55rem; }
      .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
      .grid-3 { display: grid; grid-template-columns: 1.2fr 1fr 1fr; gap: 1rem; }
      @media (max-width: 900px) {
        .grid, .grid-3 { grid-template-columns: 1fr; }
      }
      .muted { color: var(--muted); }
      .toolbar { display: flex; gap: .5rem; align-items: center; flex-wrap: wrap; }
      .toolbar input, .toolbar select, .toolbar textarea, .toolbar button {
        padding: .4rem .6rem;
        background: #0a1024;
        color: var(--text);
        border: 1px solid #3a4b78;
        border-radius: 8px;
      }
      button { background: var(--accent); color: #00122a; border: 0; border-radius: 8px; padding: .45rem .7rem; font-weight: 600; }
      label { color: var(--muted); margin-right: .25rem; }
      .panel-list { margin: 0; padding-left: 1rem; max-height: 20rem; overflow: auto; }
      .panel-list li { margin-bottom: 0.35rem; list-style: none; line-height: 1.35; display: flex; align-items: center; gap: 0.45rem; flex-wrap: wrap; }
      .detail-list { list-style: none; padding-left: 0; }
      .detail-list li { margin-bottom: 0.25rem; }
      .summary-grid { display: grid; gap: 0.4rem; grid-template-columns: 1fr 1fr; }
      .summary-cell { background: #101a34; padding: 0.4rem 0.55rem; border-radius: 8px; border: 1px solid #2b365d; min-height: 1.6rem; }
      .status-ok { color: #9dffb0; }
      .status-warn { color: #ffd98a; }
    </style>
  </head>
  <body>
    <main>
      <section>
        <h1>yolo-webui monitor</h1>
        <p class="muted">Live task queue and execution state for v3.0 distributed runner control.</p>
        <div class="toolbar">
          <label for="source-filter">Source filter</label>
          <input id="source-filter" placeholder="worker-id or mastermind id" />
          <button id="apply-filter">Save</button>
          <span id="source-status" class="muted"></span>
        </div>
      </section>
      <section class="summary-grid">
        <div class="summary-cell">Current Task: <span id="current-task"></span></div>
        <div class="summary-cell">Phase: <span id="phase"></span></div>
        <div class="summary-cell">Last Output Age: <span id="last-output-age"></span></div>
        <div class="summary-cell">Summary: <span id="status-summary"></span></div>
      </section>
      <section class="grid-3">
        <div>
          <h2>Executor Dashboard</h2>
          <ul id="workers" class="panel-list"></ul>
        </div>
        <div>
          <h2>Dependency Graph</h2>
          <pre id="graph"></pre>
        </div>
        <div>
          <h2>Task Details</h2>
          <pre id="task-details"></pre>
        </div>
      </section>
      <section class="grid">
        <div>
          <h2>Execution Queue (priority-aware)</h2>
          <ul id="queue" class="panel-list"></ul>
        </div>
        <div>
          <h2>Task Control</h2>
          <div class="toolbar" style="align-items:flex-start;">
            <label for="task-id">Task ID</label>
            <input id="task-id" placeholder="task id" />
            <label for="status-select">Status</label>
            <select id="status-select">
              <option value="open">open</option>
              <option value="in_progress">in_progress</option>
              <option value="blocked">blocked</option>
              <option value="closed">closed</option>
              <option value="failed">failed</option>
            </select>
          </div>
          <div class="toolbar">
            <label for="comment-input">Comment</label>
            <input id="comment-input" placeholder="Optional comment" style="min-width: 24rem;" />
          </div>
          <div class="toolbar">
            <label for="status-backends">Backends (comma-separated)</label>
            <input id="status-backends" placeholder="defaults to configured backends" style="min-width: 24rem;" />
          </div>
          <div class="toolbar">
            <label for="status-auth">Mastermind auth token</label>
            <input id="status-auth" placeholder="optional if server is pre-configured" />
            <button id="update-task-status">Update task status</button>
          </div>
          <p id="control-feedback" class="muted"></p>
          <p class="muted">Queue rows include quick actions for status updates.</p>
        </div>
      </section>
    </main>
    <script>
      const queueEl = document.getElementById('queue');
      const workersEl = document.getElementById('workers');
      const graphEl = document.getElementById('graph');
      const taskDetailsEl = document.getElementById('task-details');
      const currentTaskEl = document.getElementById('current-task');
      const phaseEl = document.getElementById('phase');
      const lastOutputAgeEl = document.getElementById('last-output-age');
      const statusSummaryEl = document.getElementById('status-summary');
      const sourceStatusEl = document.getElementById('source-status');
      const controlFeedbackEl = document.getElementById('control-feedback');
      const filterEl = document.getElementById('source-filter');
      const applyBtn = document.getElementById('apply-filter');
      const taskInputEl = document.getElementById('task-id');
      const statusSelectEl = document.getElementById('status-select');
      const commentInputEl = document.getElementById('comment-input');
      const statusBackendsEl = document.getElementById('status-backends');
      const statusAuthEl = document.getElementById('status-auth');
      const updateTaskStatusBtn = document.getElementById('update-task-status');
      let state = null;

      function authHeaders() {
        const token = new URLSearchParams(window.location.search).get('token') || '';
        if (!token) {
          return {};
        }
        return { Authorization: 'Bearer ' + token };
      }

      function render() {
        if (!state || !state.state) {
          return;
        }
        const snapshot = state.state || {};
        const current = snapshot.CurrentTask || 'n/a';
        const phase = snapshot.Phase || 'n/a';
        const lastOutputAge = snapshot.LastOutputAge || 'n/a';
        const statusSummary = snapshot.StatusSummary || '';
        currentTaskEl.textContent = current;
        phaseEl.textContent = phase;
        lastOutputAgeEl.textContent = lastOutputAge;
        statusSummaryEl.textContent = statusSummary;

        workersEl.innerHTML = '';
        (snapshot.WorkerSummaries || []).forEach((worker) => {
          const li = document.createElement('li');
          const details = (worker.WorkerID || 'worker') + ' => ' + (worker.Task || 'n/a') + ' (queue=' + (worker.QueuePos || 0) + ', priority=' + (worker.TaskPriority || 0) + ')';
          const status = worker.LastEvent || '';
          li.textContent = details + ' | ' + status;
          workersEl.appendChild(li);
        });
        if ((snapshot.WorkerSummaries || []).length === 0) {
          workersEl.innerHTML = '<li class="muted">no workers yet</li>';
        }

        queueEl.innerHTML = '';
        const sendStatusUpdate = (taskID, status) => {
          postControl({
            action: 'set-task-status',
            task_id: taskID,
            status: status,
            comment: commentInputEl.value || '',
            backends: splitCSV(statusBackendsEl.value),
            status_auth_token: statusAuthEl.value || '',
          });
        };

        (snapshot.Queue || []).forEach((entry) => {
          const taskID = taskIDFromLine(entry);
          const li = document.createElement('li');
          const title = document.createElement('span');
          title.textContent = entry;
          li.appendChild(title);
          if (taskID) {
            const statusSelect = document.createElement('select');
            const statuses = ['open', 'in_progress', 'blocked', 'closed', 'failed'];
            statuses.forEach((value) => {
              const option = document.createElement('option');
              option.value = value;
              option.textContent = value;
              if (value === 'blocked') {
                option.selected = true;
              }
              statusSelect.appendChild(option);
            });
            li.appendChild(statusSelect);
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = 'Set status';
            button.addEventListener('click', (event) => {
              event.preventDefault();
              sendStatusUpdate(taskID, statusSelect.value);
            });
            li.appendChild(button);
            li.addEventListener('click', () => {
              taskInputEl.value = taskID;
            });
          }
          queueEl.appendChild(li);
        });
        if ((snapshot.Queue || []).length === 0) {
          queueEl.innerHTML = '<li class="muted">no queue tasks</li>';
        }

        graphEl.textContent = (snapshot.TaskGraph || ['n/a']).join('\n');
        taskDetailsEl.textContent = (snapshot.TaskDetails || ['- no task selected']).join('\n');
      }

      function refreshConfig() {
        return fetch('/api/config', { headers: authHeaders() })
          .then((response) => response.json())
          .then((cfg) => {
            if (cfg && cfg.source !== undefined) {
              filterEl.value = cfg.source || '';
              if (!filterEl.value.trim()) {
                sourceStatusEl.textContent = 'monitor source: all';
              } else {
                sourceStatusEl.textContent = 'monitor source: ' + filterEl.value.trim();
              }
            }
          });
      }

      function loadState() {
        return fetch('/api/state', { headers: authHeaders() })
          .then((response) => response.json())
          .then((snapshot) => {
            state = snapshot;
            render();
          });
      }

      function connectWebsocket() {
        const token = new URLSearchParams(window.location.search).get('token') || '';
        const wsUrl = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws?token=' + encodeURIComponent(token);
        const socket = new WebSocket(wsUrl);
        socket.onmessage = (event) => {
          try {
            state = JSON.parse(event.data);
            render();
          } catch (_err) {}
        };
        socket.onclose = () => {
          setTimeout(connectWebsocket, 500);
        };
      }

      function splitCSV(raw) {
        return (raw || '').split(',').map((item) => item.trim()).filter(Boolean);
      }

      function taskIDFromLine(line) {
        if (!line) {
          return '';
        }
        const trimmed = String(line).trim();
        const clean = trimmed.replace(/^-+\s*/, '');
        const idx = clean.indexOf(' - ');
        if (idx <= 0) {
          return clean;
        }
        return clean.slice(0, idx).trim();
      }

      function renderControlFeedback(message, ok) {
        if (!controlFeedbackEl) {
          return;
        }
        controlFeedbackEl.textContent = message;
        controlFeedbackEl.className = ok ? 'status-ok' : 'status-warn';
      }

      function postControl(payload) {
        return fetch('/api/control', {
          method: 'POST',
          headers: Object.assign({ 'Content-Type': 'application/json' }, authHeaders()),
          body: JSON.stringify(payload),
        })
          .then((response) => {
            return response.json().then((result) => ({ result, status: response.status }));
          })
          .then(({ result }) => {
            if (result && result.status === 'ok') {
              renderControlFeedback('updated', true);
              return;
            }
            if (result && result.status === 'accepted') {
              renderControlFeedback('command accepted' + (result.command_id ? ' (' + result.command_id + ')' : ''), true);
              return;
            }
            renderControlFeedback(result && result.error ? result.error : 'control request failed', false);
          })
          .catch(() => {
            renderControlFeedback('control request failed', false);
          });
      }

      applyBtn.addEventListener('click', function() {
        const source = filterEl.value.trim();
        postControl({
          action: 'set-source',
          source: source,
        }).then(loadState);
      });

      updateTaskStatusBtn.addEventListener('click', function() {
        const taskID = taskInputEl.value.trim();
        if (!taskID) {
          renderControlFeedback('task id is required', false);
          return;
        }
        postControl({
          action: 'set-task-status',
          task_id: taskID,
          status: statusSelectEl.value || 'open',
          comment: commentInputEl.value || '',
          backends: splitCSV(statusBackendsEl.value),
          status_auth_token: statusAuthEl.value || '',
        });
      });

      refreshConfig().then(loadState).then(connectWebsocket);
      setInterval(() => { void loadState(); }, 4000);
    </script>
  </body>
</html>`
}

func normalizeDistributedBusBackend(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", "redis":
		return "redis", nil
	case "nats":
		return "nats", nil
	default:
		return "", fmt.Errorf("unsupported distributed bus backend %q (supported: redis, nats)", raw)
	}
}

func resolveWebUIDistributedBusConfig(
	repoRoot string,
	flagBackend string,
	flagAddress string,
	flagPrefix string,
	flagSource string,
	getenv func(string) string,
) (distributed.DistributedBusConfig, error) {
	configBus, err := distributed.LoadDistributedBusConfig(repoRoot)
	if err != nil {
		return distributed.DistributedBusConfig{}, err
	}
	configBus = configBus.ApplyDefaults(defaultBusBackend, defaultBusPrefix)
	if getenv == nil {
		getenv = os.Getenv
	}

	selectedBackend := strings.TrimSpace(flagBackend)
	if selectedBackend == "" {
		selectedBackend = strings.TrimSpace(getenv("YOLO_DISTRIBUTED_BUS_BACKEND"))
	}
	if selectedBackend == "" {
		selectedBackend = configBus.Backend
	}
	selectedBackend, err = normalizeDistributedBusBackend(selectedBackend)
	if err != nil {
		return distributed.DistributedBusConfig{}, err
	}

	selectedAddress := strings.TrimSpace(flagAddress)
	if selectedAddress == "" {
		selectedAddress = strings.TrimSpace(getenv("YOLO_DISTRIBUTED_BUS_ADDRESS"))
	}
	if selectedAddress == "" {
		selectedAddress = configBus.Address
	}
	if selectedAddress == "" {
		return distributed.DistributedBusConfig{}, fmt.Errorf("--distributed-bus-address is required")
	}

	selectedPrefix := strings.TrimSpace(flagPrefix)
	if selectedPrefix == "" {
		selectedPrefix = strings.TrimSpace(getenv("YOLO_DISTRIBUTED_BUS_PREFIX"))
	}
	if selectedPrefix == "" {
		selectedPrefix = configBus.Prefix
	}
	if selectedPrefix == "" {
		selectedPrefix = defaultBusPrefix
	}

	selectedSource := strings.TrimSpace(flagSource)
	if selectedSource == "" {
		selectedSource = strings.TrimSpace(getenv("YOLO_MONITOR_SOURCE_ID"))
	}
	if selectedSource == "" {
		selectedSource = configBus.Source
	}

	configBus.Backend = selectedBackend
	configBus.Address = selectedAddress
	configBus.Prefix = selectedPrefix
	configBus.Source = selectedSource
	return configBus, nil
}
