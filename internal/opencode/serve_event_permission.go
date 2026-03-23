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

// ServeEventPermissionRequest holds the permission request extracted from an
// OpenCode serve SSE event of type "permission.requested".
type ServeEventPermissionRequest struct {
	// ID is the unique request identifier used when posting the response.
	ID string
	// ToolName is the name of the tool requesting permission.
	ToolName string
	// Options lists the selectable response options provided by the server.
	Options []ServePermissionOption
}

// ServePermissionOption represents one selectable option in a permission request.
type ServePermissionOption struct {
	// Kind is the option kind, e.g. "allow_once", "allow_always", "reject_once".
	Kind string
	// OptionID is the opaque identifier sent back to the server when selecting this option.
	OptionID string
}

// DetectServeEventPermissionRequest inspects a decoded SSE event from the OpenCode
// serve stream and determines whether it is a permission request.
//
// Returns (request, true) when the event is "permission.requested" and contains
// a non-empty request ID. Returns (nil, false) for all other events.
func DetectServeEventPermissionRequest(event contracts.SSEEvent) (*ServeEventPermissionRequest, bool) {
	data := strings.TrimSpace(event.Data)
	if data == "" {
		return nil, false
	}

	var payload struct {
		Type       string `json:"type"`
		Properties struct {
			ID       string `json:"id"`
			ToolName string `json:"toolName"`
			Options  []struct {
				Kind     string `json:"kind"`
				OptionID string `json:"optionId"`
			} `json:"options"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return nil, false
	}

	if strings.TrimSpace(payload.Type) != "permission.requested" {
		return nil, false
	}

	id := strings.TrimSpace(payload.Properties.ID)
	if id == "" {
		return nil, false
	}

	opts := make([]ServePermissionOption, 0, len(payload.Properties.Options))
	for _, o := range payload.Properties.Options {
		opts = append(opts, ServePermissionOption{
			Kind:     o.Kind,
			OptionID: o.OptionID,
		})
	}

	return &ServeEventPermissionRequest{
		ID:       id,
		ToolName: strings.TrimSpace(payload.Properties.ToolName),
		Options:  opts,
	}, true
}

// RespondServePermissionRequest sends an allow response to the OpenCode serve API
// for the given permission request. It selects "allow_once" if available,
// falling back to "allow_always". An error is returned when no allow option exists
// or the server rejects the response.
func RespondServePermissionRequest(ctx context.Context, client *http.Client, baseURL string, req *ServeEventPermissionRequest) error {
	if req == nil {
		return errors.New("nil permission request")
	}

	optionID := selectAllowOption(req.Options)
	if optionID == "" {
		return fmt.Errorf("no allow option available for permission request %q", req.ID)
	}

	body, err := json.Marshal(map[string]string{"optionId": optionID})
	if err != nil {
		return err
	}

	url := strings.TrimRight(baseURL, "/") + "/permission/" + req.ID
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
		return fmt.Errorf("permission response returned %d", resp.StatusCode)
	}
	return nil
}

// selectAllowOption returns the option ID for the best available allow option,
// preferring "allow_once" over "allow_always". Returns "" when neither is present.
func selectAllowOption(options []ServePermissionOption) string {
	allowAlways := ""
	for _, o := range options {
		switch o.Kind {
		case "allow_once":
			return o.OptionID
		case "allow_always":
			if allowAlways == "" {
				allowAlways = o.OptionID
			}
		}
	}
	return allowAlways
}
