package beads

import (
	"encoding/json"
	"strings"

	"yolo-runner/internal/runner"
)

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
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
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
	output, err := a.runner.Run("bd", "show", rootID, "--json")
	if err != nil {
		return runner.Issue{}, err
	}
	var issues []runner.Issue
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
		return runner.Issue{}, err
	}
	if len(issues) == 0 {
		return runner.Issue{}, nil
	}
	return issues[0], nil
}

func (a *Adapter) readyFallback(rootID string) (runner.Issue, error) {
	output, err := a.runner.Run("bd", "show", rootID, "--json")
	if err != nil {
		return runner.Issue{}, err
	}
	var issues []runner.Issue
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
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
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
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
		return trimmed[:maxLen]
	}
	return trimmed
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
