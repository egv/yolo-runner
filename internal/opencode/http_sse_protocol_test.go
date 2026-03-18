package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestFakeHTTPSSEHarnessSupportsOpenCodeStyleHTTPAndSSEFlow(t *testing.T) {
	harness := contracts.NewFakeHTTPSSEHarness()
	t.Cleanup(harness.Close)

	harness.SetHealthStatus(http.StatusNoContent)
	harness.QueueJSONResponse("/session", http.StatusCreated, map[string]any{"id": "session-1"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := contracts.CheckHTTPReadiness(ctx, harness.Client(), contracts.HTTPReadinessCheck{
		Endpoint: harness.HealthURL(),
	}); err != nil {
		t.Fatalf("health check: %v", err)
	}

	sessionID, events, err := runOpenCodeHTTPSSEFlow(ctx, harness.Client(), harness.URL("/session"), harness.SSEURL(), "ship it", func() error {
		return harness.SendSSE(contracts.SSEEvent{
			Event: "message",
			Data:  `{"type":"token","text":"hello"}`,
		})
	})
	if err != nil {
		t.Fatalf("run http/sse flow: %v", err)
	}
	if sessionID != "session-1" {
		t.Fatalf("expected session-1, got %q", sessionID)
	}
	if len(events) != 1 {
		t.Fatalf("expected one streamed event, got %#v", events)
	}
	if events[0].Event != "message" {
		t.Fatalf("expected message event, got %#v", events[0])
	}
	if events[0].Data != `{"type":"token","text":"hello"}` {
		t.Fatalf("unexpected event data %#v", events[0])
	}

	recorded := harness.Requests("/session")
	if len(recorded) != 1 {
		t.Fatalf("expected one session request, got %#v", recorded)
	}
	if strings.TrimSpace(string(recorded[0].Body)) != `{"prompt":"ship it"}` {
		t.Fatalf("unexpected request body %q", string(recorded[0].Body))
	}
}

func runOpenCodeHTTPSSEFlow(ctx context.Context, client *http.Client, sessionURL string, sseURL string, prompt string, emit func() error) (string, []contracts.SSEEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL, strings.NewReader(`{"prompt":"`+prompt+`"}`))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return "", nil, err
	}

	streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, http.NoBody)
	if err != nil {
		return "", nil, err
	}
	streamResp, err := client.Do(streamReq)
	if err != nil {
		return "", nil, err
	}
	defer streamResp.Body.Close()

	if emit == nil {
		return "", nil, io.EOF
	}

	eventsCh := make(chan contracts.SSEEvent, 1)
	readErrCh := make(chan error, 1)
	emitErrCh := make(chan error, 1)
	go func() {
		event, readErr := readSingleSSEEvent(streamResp.Body)
		if readErr != nil {
			readErrCh <- readErr
			return
		}
		eventsCh <- event
	}()

	go func() {
		emitErrCh <- emit()
	}()

	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case err := <-emitErrCh:
		if err != nil {
			return "", nil, err
		}
	}

	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case err := <-readErrCh:
		if err != nil {
			return "", nil, err
		}
	case event := <-eventsCh:
		return created.ID, []contracts.SSEEvent{event}, nil
	}

	return "", nil, io.EOF
}

func readSingleSSEEvent(body io.Reader) (contracts.SSEEvent, error) {
	scanner := bufio.NewScanner(body)
	event := contracts.SSEEvent{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if event.Event != "" || event.Data != "" || event.ID != "" {
				return event, nil
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			event.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			if event.Data != "" {
				event.Data += "\n"
			}
			event.Data += strings.TrimPrefix(line, "data: ")
		case strings.HasPrefix(line, "id: "):
			event.ID = strings.TrimPrefix(line, "id: ")
		}
	}
	if err := scanner.Err(); err != nil {
		return contracts.SSEEvent{}, err
	}
	return contracts.SSEEvent{}, io.EOF
}
