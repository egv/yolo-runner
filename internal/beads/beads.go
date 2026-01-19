package beads

import (
	"encoding/json"

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
		return runner.Issue{}, nil
	}
	return issues[0], nil
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
	args := []string{"bd", "update", id, "--status", status}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	_, err := a.runner.Run(args...)
	return err
}

func (a *Adapter) Close(id string) error {
	_, err := a.runner.Run("bd", "close", id)
	return err
}

func (a *Adapter) Sync() error {
	_, err := a.runner.Run("bd", "sync")
	return err
}
