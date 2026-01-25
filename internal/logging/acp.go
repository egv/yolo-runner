package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type ACPRequestEntry struct {
	Timestamp   string `json:"timestamp"`
	IssueID     string `json:"issue_id"`
	RequestType string `json:"request_type"`
	Decision    string `json:"decision"`
	Message     string `json:"message,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

func AppendACPRequest(logPath string, entry ACPRequestEntry) error {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(payload, '\n'))
	return err
}
