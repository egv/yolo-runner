package logging

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type summaryEntry struct {
	Timestamp string `json:"timestamp"`
	IssueID   string `json:"issue_id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	CommitSHA string `json:"commit_sha"`
}

func AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error {
	logPath := filepath.Join(repoRoot, "runner-logs", "beads_yolo_runner.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	if commitSHA == "" {
		commitSHA = readHeadSHA(repoRoot)
	}
	entry := summaryEntry{
		Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		IssueID:   issueID,
		Title:     title,
		Status:    status,
		CommitSHA: commitSHA,
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
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func readHeadSHA(repoRoot string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
