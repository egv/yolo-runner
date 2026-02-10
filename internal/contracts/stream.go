package contracts

import (
	"bufio"
	"encoding/json"
	"io"
	"time"
)

type EventStream struct {
	w io.Writer
}

func NewEventStream(writer io.Writer) *EventStream {
	return &EventStream{w: writer}
}

func (s *EventStream) Write(event Event) error {
	if s == nil || s.w == nil {
		return nil
	}
	line, err := MarshalEventJSONL(event)
	if err != nil {
		return err
	}
	_, err = io.WriteString(s.w, line)
	return err
}

type EventDecoder struct {
	scanner *bufio.Scanner
}

func NewEventDecoder(reader io.Reader) *EventDecoder {
	if reader == nil {
		return &EventDecoder{}
	}
	return &EventDecoder{scanner: bufio.NewScanner(reader)}
}

func (d *EventDecoder) Next() (Event, error) {
	if d == nil || d.scanner == nil {
		return Event{}, io.EOF
	}
	if !d.scanner.Scan() {
		if err := d.scanner.Err(); err != nil {
			return Event{}, err
		}
		return Event{}, io.EOF
	}
	return ParseEventJSONLLine(d.scanner.Bytes())
}

func ParseEventJSONLLine(line []byte) (Event, error) {
	var payload struct {
		Type      string            `json:"type"`
		TaskID    string            `json:"task_id"`
		TaskTitle string            `json:"task_title"`
		WorkerID  string            `json:"worker_id"`
		ClonePath string            `json:"clone_path"`
		QueuePos  int               `json:"queue_pos"`
		Message   string            `json:"message"`
		Metadata  map[string]string `json:"metadata"`
		TS        string            `json:"ts"`
	}
	if err := json.Unmarshal(line, &payload); err != nil {
		return Event{}, err
	}
	timestamp := time.Time{}
	if payload.TS != "" {
		parsed, err := time.Parse(time.RFC3339, payload.TS)
		if err != nil {
			return Event{}, err
		}
		timestamp = parsed
	}
	return Event{
		Type:      EventType(payload.Type),
		TaskID:    payload.TaskID,
		TaskTitle: payload.TaskTitle,
		WorkerID:  payload.WorkerID,
		ClonePath: payload.ClonePath,
		QueuePos:  payload.QueuePos,
		Message:   payload.Message,
		Metadata:  payload.Metadata,
		Timestamp: timestamp,
	}, nil
}
