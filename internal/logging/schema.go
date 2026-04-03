package logging

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type LoggingSchemaFields struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Component string `json:"component"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
}

func populateRequiredLogFields(fields LoggingSchemaFields, defaultTaskID string) LoggingSchemaFields {
	if fields.Timestamp == "" {
		fields.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(fields.Level) == "" {
		fields.Level = "info"
	}
	if strings.TrimSpace(fields.Component) == "" {
		fields.Component = "yolo-agent"
	}
	if strings.TrimSpace(fields.TaskID) == "" {
		fields.TaskID = defaultTaskID
	}
	if strings.TrimSpace(fields.RunID) == "" {
		fields.RunID = fields.TaskID
	}
	return fields
}

func ValidateStructuredLogLine(line []byte) error {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return fmt.Errorf("log line is empty")
	}

	entry := map[string]interface{}{}
	if err := json.Unmarshal(line, &entry); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}

	required := []string{
		"timestamp",
		"level",
		"component",
		"task_id",
		"run_id",
	}
	for _, field := range required {
		value, ok := entry[field]
		if !ok {
			return fmt.Errorf("missing required field %q", field)
		}
		raw, ok := value.(string)
		if !ok || strings.TrimSpace(raw) == "" {
			return fmt.Errorf("required field %q must be a non-empty string", field)
		}
		if field == "timestamp" {
			if _, err := time.Parse(time.RFC3339, raw); err != nil {
				return fmt.Errorf("invalid timestamp %q: %w", raw, err)
			}
		}
	}

	return nil
}
