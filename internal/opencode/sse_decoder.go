package opencode

import (
	"bufio"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// decodeNextSSEFrame reads the next complete SSE frame from the scanner.
// Returns (event, true, nil) when a frame is decoded.
// Returns (SSEEvent{}, false, nil) at EOF without a complete frame.
// Returns (SSEEvent{}, false, err) if the scanner reports an error.
func decodeNextSSEFrame(scanner *bufio.Scanner) (contracts.SSEEvent, bool, error) {
	var event contracts.SSEEvent
	hasField := false

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// Blank line dispatches the current frame.
			if hasField {
				return event, true, nil
			}
			// Leading blank line — continue accumulating.
			continue
		}

		// SSE comment — ignore.
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			event.Event = value
			hasField = true
		case "data":
			if event.Data != "" {
				event.Data += "\n"
			}
			event.Data += value
			hasField = true
		case "id":
			event.ID = value
			hasField = true
		}
	}

	if err := scanner.Err(); err != nil {
		return contracts.SSEEvent{}, false, err
	}
	return contracts.SSEEvent{}, false, nil
}
