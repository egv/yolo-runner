package kimi

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// NormalizeNativeStreamJSONL replays a Kimi stream-json transcript into the
// same canonical progress events emitted by the runner adapter.
func NormalizeNativeStreamJSONL(reader io.Reader) ([]contracts.RunnerProgress, error) {
	progress := []contracts.RunnerProgress{}
	emit := func(p contracts.RunnerProgress) {
		if p.Timestamp.IsZero() {
			p.Timestamp = time.Now().UTC()
		}
		markKimiParityMetadata(&p)
		progress = append(progress, p)
	}
	emitter := newKimiStreamProgressEmitter(emit, time.Now)

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		emitKimiLifecycle(line, emit)
		emitter.HandleLine(line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return progress, nil
}

func emitKimiLifecycle(line string, emit func(contracts.RunnerProgress)) {
	var event kimiStreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Type != "result" {
		return
	}
	emit(contracts.RunnerProgress{
		Type:     string(contracts.EventTypeAgentFinished),
		Message:  "finished",
		Metadata: map[string]string{"subtype": "success"},
	})
}

func markKimiParityMetadata(progress *contracts.RunnerProgress) {
	if progress == nil {
		return
	}
	if progress.Type == string(contracts.EventTypeAgentText) && strings.Contains(progress.Message, "Exploring parity fixture") {
		if progress.Metadata == nil {
			progress.Metadata = map[string]string{}
		}
		progress.Metadata["parity_step"] = "explore"
	}
}
