package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// ServeEventQuestion holds the question extracted from an OpenCode serve SSE
// event of type "assistant.question".
type ServeEventQuestion struct {
	// ID is the unique request identifier used when posting the response.
	ID string
	// Prompt is the question text presented to the user.
	Prompt string
	// Context is optional additional context provided alongside the question.
	Context string
	// Options lists selectable answers when the question offers fixed choices.
	Options []string
}

// DetectServeEventQuestion inspects a decoded SSE event from the OpenCode serve
// stream and determines whether it is an interactive question prompt.
//
// Returns (question, true) when the event is "assistant.question" and contains
// a non-empty request ID. Returns (nil, false) for all other events.
func DetectServeEventQuestion(event contracts.SSEEvent) (*ServeEventQuestion, bool) {
	data := strings.TrimSpace(event.Data)
	if data == "" {
		return nil, false
	}

	var payload struct {
		Type       string `json:"type"`
		Properties struct {
			ID      string   `json:"id"`
			Prompt  string   `json:"prompt"`
			Context string   `json:"context"`
			Options []string `json:"options"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return nil, false
	}

	if strings.TrimSpace(payload.Type) != "assistant.question" {
		return nil, false
	}

	id := strings.TrimSpace(payload.Properties.ID)
	if id == "" {
		return nil, false
	}

	return &ServeEventQuestion{
		ID:      id,
		Prompt:  strings.TrimSpace(payload.Properties.Prompt),
		Context: strings.TrimSpace(payload.Properties.Context),
		Options: payload.Properties.Options,
	}, true
}

// RespondServeQuestion sends an answer to the OpenCode serve API for the given
// question request. It POSTs the answer text to /question/<id>.
func RespondServeQuestion(ctx context.Context, client *http.Client, baseURL string, req *ServeEventQuestion, answer string) error {
	if req == nil {
		return errors.New("nil question request")
	}

	body, err := json.Marshal(map[string]string{"answer": answer})
	if err != nil {
		return err
	}

	url := strings.TrimRight(baseURL, "/") + "/question/" + req.ID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpClient := client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("question response returned %d", resp.StatusCode)
	}
	return nil
}
