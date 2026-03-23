package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredLoggerWritesJSONLWithRequiredFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewStructuredLogger(buf, "debug", LoggingSchemaFields{
		Component: "runner",
		TaskID:    "task-1",
		RunID:     "run-1",
	})

	err := logger.Log("info", map[string]interface{}{
		"issue_id": "task-1",
		"message":  "runner started",
		"status":   "started",
	})
	if err != nil {
		t.Fatalf("log error: %v", err)
	}

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected one log line")
	}

	if err := ValidateStructuredLogLine([]byte(line)); err != nil {
		t.Fatalf("expected structured line, got: %v", err)
	}

	entry := map[string]interface{}{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if entry["message"] != "runner started" {
		t.Fatalf("expected message field, got %#v", entry["message"])
	}
	if entry["status"] != "started" {
		t.Fatalf("expected status field, got %#v", entry["status"])
	}
}

func TestStructuredLoggerFiltersByLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewStructuredLogger(buf, "warn", LoggingSchemaFields{
		Component: "runner",
		TaskID:    "task-2",
		RunID:     "run-2",
	})

	if err := logger.Log("debug", map[string]interface{}{"message": "too noisy"}); err != nil {
		t.Fatalf("log error: %v", err)
	}
	if err := logger.Log("info", map[string]interface{}{"message": "still noisy"}); err != nil {
		t.Fatalf("log error: %v", err)
	}
	if err := logger.Log("warn", map[string]interface{}{"message": "needs attention"}); err != nil {
		t.Fatalf("log error: %v", err)
	}
	if err := logger.Log("error", map[string]interface{}{"message": "failed hard"}); err != nil {
		t.Fatalf("log error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 emitted lines, got %d", len(lines))
	}

	var entries []map[string]interface{}
	for _, line := range lines {
		entry := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if err := ValidateStructuredLogLine([]byte(line)); err != nil {
			t.Fatalf("expected structured line, got: %v", err)
		}
		entries = append(entries, entry)
	}

	if entries[0]["message"] != "needs attention" {
		t.Fatalf("expected first visible entry to be warn, got %#v", entries[0]["message"])
	}
	if entries[1]["message"] != "failed hard" {
		t.Fatalf("expected second visible entry to be error, got %#v", entries[1]["message"])
	}
}
