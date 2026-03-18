package beads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/runner"
)

func traceJSONParse(operation string, data []byte, target interface{}) error {
	err := json.Unmarshal(data, target)
	if err == nil {
		return nil
	}

	if payload := extractJSONPayload(data); len(payload) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		if err := decoder.Decode(target); err == nil {
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "JSON parse error in %s: %v\n", operation, err)
	fmt.Fprintf(os.Stderr, "First 200 bytes: %q\n", string(data[:min(200, len(data))]))
	return err
}

func extractJSONPayload(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		return trimmed
	}
	for _, marker := range [][]byte{[]byte("\n["), []byte("\n{"), []byte("\r\n["), []byte("\r\n{")} {
		if idx := bytes.Index(data, marker); idx >= 0 {
			return bytes.TrimSpace(data[idx+len(marker)-1:])
		}
	}
	if idx := bytes.IndexAny(data, "[{"); idx >= 0 {
		return bytes.TrimSpace(data[idx:])
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type Runner interface {
	Run(args ...string) (string, error)
}

type Adapter struct {
	runner Runner
}

func New(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

type readyResponse struct {
	Items []runner.Issue
}

func (a *Adapter) Ready(rootID string) (runner.Issue, error) {
	output, err := a.runner.Run("bd", "ready", "--parent", rootID, "--json")
	if err != nil {
		return runner.Issue{}, err
	}
	var issues []runner.Issue
	if err := traceJSONParse("Ready", []byte(output), &issues); err != nil {
		return runner.Issue{}, err
	}
	if len(issues) == 0 {
		return a.readyFallback(rootID)
	}
	if len(issues) == 1 {
		return issues[0], nil
	}
	return runner.Issue{
		ID:        rootID,
		IssueType: "epic",
		Status:    "open",
		Children:  issues,
	}, nil
}

func (a *Adapter) Tree(rootID string) (runner.Issue, error) {
	issues, err := a.listTree(rootID)
	if err != nil {
		return runner.Issue{}, err
	}
	if len(issues) > 0 {
		if len(issues) == 1 {
			return issues[0], nil
		}
		for _, issue := range issues {
			if issue.ID == rootID {
				return issue, nil
			}
		}
		return runner.Issue{
			ID:        rootID,
			IssueType: "epic",
			Status:    "open",
			Children:  issues,
		}, nil
	}

	output, err := a.runner.Run("bd", "show", rootID, "--json")
	if err != nil {
		return runner.Issue{}, err
	}
	var fallback []runner.Issue
	if err := json.Unmarshal([]byte(output), &fallback); err != nil {
		return runner.Issue{}, err
	}
	if len(fallback) == 0 {
		return runner.Issue{}, nil
	}
	return fallback[0], nil
}

func (a *Adapter) listTree(rootID string) ([]runner.Issue, error) {
	output, err := a.runner.Run("bd", "list", "--parent", rootID, "--json")
	if err != nil {
		return nil, err
	}
	var issues []runner.Issue
	if err := traceJSONParse("listTree", []byte(output), &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func (a *Adapter) readyFallback(rootID string) (runner.Issue, error) {
	output, err := a.runner.Run("bd", "show", rootID, "--json")
	if err != nil {
		return runner.Issue{}, err
	}
	var issues []runner.Issue
	if err := traceJSONParse("readyFallback", []byte(output), &issues); err != nil {
		return runner.Issue{}, err
	}
	if len(issues) == 0 {
		return runner.Issue{}, nil
	}
	issue := issues[0]
	if issue.Status != "open" {
		return runner.Issue{}, nil
	}
	if issue.IssueType == "epic" || issue.IssueType == "molecule" {
		return runner.Issue{}, nil
	}
	return issue, nil
}

type showIssue struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Status             string `json:"status"`
}

func (a *Adapter) Show(id string) (runner.Bead, error) {
	output, err := a.runner.Run("bd", "show", id, "--json")
	if err != nil {
		return runner.Bead{}, err
	}
	var issues []showIssue
	if err := traceJSONParse("Show", []byte(output), &issues); err != nil {
		return runner.Bead{}, err
	}
	if len(issues) == 0 {
		return runner.Bead{}, nil
	}
	issue := issues[0]
	return runner.Bead{
		ID:                 issue.ID,
		Title:              issue.Title,
		Description:        issue.Description,
		AcceptanceCriteria: issue.AcceptanceCriteria,
		Status:             issue.Status,
	}, nil
}

func (a *Adapter) UpdateStatus(id string, status string) error {
	_, err := a.runner.Run("bd", "update", id, "--status", status)
	return err
}

func (a *Adapter) UpdateStatusWithReason(id string, status string, reason string) error {
	if err := a.UpdateStatus(id, status); err != nil {
		return err
	}
	sanitized := sanitizeReason(reason)
	if sanitized == "" {
		return nil
	}
	_, err := a.runner.Run("bd", "update", id, "--notes", sanitized)
	return err
}

func sanitizeReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\r", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\n", "; ")
	const maxLen = 500
	if len(trimmed) > maxLen {
		return truncateRunes(trimmed, maxLen)
	}
	return trimmed
}

func truncateRunes(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range input {
		if count == maxRunes {
			return input[:i]
		}
		count++
	}
	return input
}

func (a *Adapter) Close(id string) error {
	_, err := a.runner.Run("bd", "close", id)
	return err
}

func (a *Adapter) CloseEligible() error {
	_, err := a.runner.Run("bd", "epic", "close-eligible")
	return err
}

func (a *Adapter) Sync() error {
	_, err := a.runner.Run("bd", "sync")
	return err
}

// IsAvailable checks if beads is available in the repository
func IsAvailable(repoRoot string) bool {
	beadsDir := filepath.Join(repoRoot, ".beads")
	_, err := os.Stat(beadsDir)
	return err == nil
}
