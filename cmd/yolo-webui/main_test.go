package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/version"
)

func TestRunMainSupportsVersionFlag(t *testing.T) {
	original := version.Version
	version.Version = "webui-version-test"
	t.Cleanup(func() {
		version.Version = original
	})

	code := RunMain([]string{"--version"}, nil)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
}

func TestNormalizeDistributedBusBackendRejectsUnknownBackend(t *testing.T) {
	_, err := normalizeDistributedBusBackend("unknown")
	if err == nil {
		t.Fatalf("expected backend validation error")
	}
}

func TestRunMainRejectsMissingBusAddress(t *testing.T) {
	run := func(_ context.Context, _ runConfig) error {
		t.Fatalf("run function should not be called when validation fails")
		return nil
	}
	code := RunMain([]string{"--distributed-bus-backend", "redis"}, run)
	if code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
}

func TestRunMainRejectsUnknownBusBackend(t *testing.T) {
	run := func(_ context.Context, _ runConfig) error {
		t.Fatalf("run function should not be called when validation fails")
		return nil
	}
	code := RunMain([]string{"--distributed-bus-backend", "unknown", "--distributed-bus-address", "mem://unit"}, run)
	if code != 1 {
		t.Fatalf("expected code 1, got %d", code)
	}
}

func TestRunMainParsesMonitorSourceAndListenAddress(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--distributed-bus-backend", "redis",
		"--distributed-bus-address", "mem://unit",
		"--distributed-bus-prefix", "unit",
		"--events-bus-source", "worker-1",
		"--listen", ":9010",
	}, run)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.busSource != "worker-1" {
		t.Fatalf("expected busSource=worker-1, got %q", got.busSource)
	}
	if got.busPrefix != "unit" {
		t.Fatalf("expected busPrefix=unit, got %q", got.busPrefix)
	}
}

func TestRunMainParsesTaskStatusControlFlags(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--distributed-bus-backend", "redis",
		"--distributed-bus-address", "mem://unit",
		"--task-status-auth-token", "token-1",
		"--task-status-backends", "tk, linear,tk,github",
	}, run)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.taskStatusAuthToken != "token-1" {
		t.Fatalf("expected taskStatusAuthToken=token-1, got %q", got.taskStatusAuthToken)
	}
	if got.taskStatusBackends == nil || len(got.taskStatusBackends) != 3 {
		t.Fatalf("expected taskStatusBackends with three values, got %#v", got.taskStatusBackends)
	}
	if got.taskStatusBackends[0] != "github" || got.taskStatusBackends[1] != "linear" || got.taskStatusBackends[2] != "tk" {
		t.Fatalf("expected normalized and deduped backends, got %#v", got.taskStatusBackends)
	}
}

func TestWebUIAuthProtectsAPIHandlers(t *testing.T) {
	state := newWebUIState("token", "", distributed.NewMemoryBus())
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	state.handleAPIConfig(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer token")
	rec = httptest.NewRecorder()
	state.handleAPIConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected success with token header, got %d", rec.Code)
	}
}

func TestWebUIPostConfigUpdatesSource(t *testing.T) {
	state := newWebUIState("", "", distributed.NewMemoryBus())
	body := strings.NewReader(`{"source":"worker-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	rec := httptest.NewRecorder()
	state.handleAPIConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	payload := statePayload{}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	if payload.Config.Source != "worker-1" {
		t.Fatalf("expected source to update to worker-1, got %q", payload.Config.Source)
	}
	if got := state.currentConfig().Source; got != "worker-1" {
		t.Fatalf("expected source to update in state, got %q", got)
	}
}

func TestWebUIIndexRendersReactShell(t *testing.T) {
	state := newWebUIState("", "", distributed.NewMemoryBus())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	state.handleIndex(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="root"></div>`) {
		t.Fatalf("expected react mount root element, got %q", body)
	}
	if !strings.Contains(body, `src="/app.js"`) {
		t.Fatalf("expected /app.js script reference, got %q", body)
	}
}

func TestWebUIAppJSRouteServesReactBundle(t *testing.T) {
	state := newWebUIState("", "", distributed.NewMemoryBus())
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	state.handleAppJS(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "React.createElement") {
		t.Fatalf("expected react-based app bundle, got %q", body)
	}
}

func TestWebUIAppJSIncludesTUIParityPanels(t *testing.T) {
	state := newWebUIState("", "", distributed.NewMemoryBus())
	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	state.handleAppJS(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	requiredLabels := []string{
		"Status Bar",
		"Run Parameters",
		"Performance",
		"Executor Dashboard",
		"Landing Queue",
		"Triage",
	}
	for _, label := range requiredLabels {
		if !strings.Contains(body, label) {
			t.Fatalf("expected app bundle to include %q panel", label)
		}
	}
}

func TestParseMonitorEnvelopeSkipsWrongSourceFilter(t *testing.T) {
	event, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "worker-2", "", distributed.MonitorEventPayload{
		Event: contracts.Event{Type: contracts.EventTypeTaskStarted},
	})
	if err != nil {
		t.Fatalf("create monitor envelope: %v", err)
	}
	_, ok, err := parseMonitorEnvelope(event, "worker-1")
	if err != nil {
		t.Fatalf("parse monitor envelope failed: %v", err)
	}
	if ok {
		t.Fatalf("expected source-filtered envelope to be skipped")
	}
}

func TestParseMonitorEnvelopeReturnsMonitorEvent(t *testing.T) {
	event, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "worker-1", "", distributed.MonitorEventPayload{
		Event: contracts.Event{
			Type:      contracts.EventTypeTaskStarted,
			TaskID:    "task-1",
			TaskTitle: "Filtered task",
		},
	})
	if err != nil {
		t.Fatalf("create monitor envelope: %v", err)
	}
	parsed, ok, err := parseMonitorEnvelope(event, "")
	if err != nil {
		t.Fatalf("parse monitor envelope failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected envelope to be accepted")
	}
	if parsed.TaskID != "task-1" || parsed.TaskTitle != "Filtered task" {
		t.Fatalf("unexpected parsed event %#v", parsed)
	}
}

func TestWebUIConsumesMonitorEventsAndAppliesSourceFilter(t *testing.T) {
	bus := distributed.NewMemoryBus()
	state := newWebUIState("", "worker-1", bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go state.consumeMonitorEvents(ctx, "unit")

	subject := distributed.DefaultEventSubjects("unit").MonitorEvent
	time.Sleep(20 * time.Millisecond)
	mismatch, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "worker-2", "", distributed.MonitorEventPayload{
		Event: contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-2", TaskTitle: "Ignored task"},
	})
	if err != nil {
		t.Fatalf("build mismatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subject, mismatch); err != nil {
		t.Fatalf("publish mismatch event: %v", err)
	}
	if got := state.currentState().CurrentTask; got != "n/a" {
		t.Fatalf("expected no initial task before matching event, got %q", got)
	}

	match, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "worker-1", "", distributed.MonitorEventPayload{
		Event: contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "Accepted task"},
	})
	if err != nil {
		t.Fatalf("build match envelope: %v", err)
	}
	if err := bus.Publish(ctx, subject, match); err != nil {
		t.Fatalf("publish match event: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for {
		if state.currentState().CurrentTask == "task-1 - Accepted task" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected task id to update from monitor bus, got %q", state.currentState().CurrentTask)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebUIControlSetTaskStatusPublishesStatusUpdate(t *testing.T) {
	bus := distributed.NewMemoryBus()
	state := newWebUIState("", "", bus)
	state.subjects = distributed.DefaultEventSubjects("unit")
	state.taskStatusBackends = []string{"tk", "github"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, unsubscribe, err := bus.Subscribe(ctx, distributed.DefaultEventSubjects("unit").TaskStatusUpdate)
	if err != nil {
		t.Fatalf("subscribe status update: %v", err)
	}
	defer unsubscribe()

	req := controlRequest{
		Action:          "set-task-status",
		TaskID:          "task-9",
		Status:          "blocked",
		Comment:         "paused",
		StatusAuthToken: "not-used",
		Metadata: map[string]string{
			"owner": "ui",
		},
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/api/control", strings.NewReader(string(body)))
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	state.handleAPIControl(rec, httpReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", rec.Code)
	}
	var response controlResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode control response: %v", err)
	}
	if response.Status != "accepted" {
		t.Fatalf("expected accepted control response, got %#v", response)
	}

	var envelope distributed.EventEnvelope
	select {
	case env, ok := <-updates:
		if !ok {
			t.Fatalf("status update channel closed")
		}
		envelope = env
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for task status update event")
	}
	payload := distributed.TaskStatusUpdatePayload{}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode task status update payload: %v", err)
	}
	if payload.TaskID != "task-9" {
		t.Fatalf("expected task-id task-9, got %q", payload.TaskID)
	}
	if payload.Status != contracts.TaskStatusBlocked {
		t.Fatalf("expected status blocked, got %q", payload.Status)
	}
	if payload.Comment != "paused" {
		t.Fatalf("expected comment paused, got %q", payload.Comment)
	}
	if payload.Metadata["owner"] != "ui" {
		t.Fatalf("expected metadata owner=ui, got %#v", payload.Metadata)
	}
	if payload.AuthToken != "" {
		t.Fatalf("expected empty auth token when none configured")
	}
}

func TestWebUIControlSetTaskStatusRequiresAuthToken(t *testing.T) {
	bus := distributed.NewMemoryBus()
	state := newWebUIState("", "", bus)
	state.subjects = distributed.DefaultEventSubjects("unit")
	state.taskStatusAuthToken = "secret"

	body := strings.NewReader(`{"action":"set-task-status","task_id":"task-1","status":"closed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/control", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	state.handleAPIControl(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without token when server configured, got %d", rec.Code)
	}

	body = strings.NewReader(`{"action":"set-task-status","task_id":"task-1","status":"closed","status_auth_token":"bad"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/control", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	state.handleAPIControl(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized with wrong token, got %d", rec.Code)
	}

	body = strings.NewReader(`{"action":"set-task-status","task_id":"task-1","status":"closed","status_auth_token":"secret"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/control", body)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	state.handleAPIControl(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted with valid token, got %d", rec.Code)
	}
}
