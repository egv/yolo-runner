package beads

import (
	"os"
	"path/filepath"

	"github.com/egv/yolo-runner/v2/internal/runner"
)

// RustAdapter provides beads_rust (br) CLI integration
// This is the Rust port of beads with SQLite + JSONL storage
// See: https://github.com/Dicklesworthstone/beads_rust
type RustAdapter struct {
	runner Runner
}

func (a *RustAdapter) run(args ...string) (string, error) {
	command := append([]string{"br", "--no-daemon"}, args...)
	return a.runner.Run(command...)
}

// NewRustAdapter creates a new beads_rust adapter
func NewRustAdapter(runner Runner) *RustAdapter {
	return &RustAdapter{runner: runner}
}

// Ready returns the next ready issue under the given root
func (a *RustAdapter) Ready(rootID string) (runner.Issue, error) {
	output, err := a.run("ready", "--parent", rootID, "--json")
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

// Tree returns the full issue tree for a root ID
func (a *RustAdapter) Tree(rootID string) (runner.Issue, error) {
	issues, err := a.listTree(rootID)
	if err != nil {
		return runner.Issue{}, err
	}
	if len(issues) > 0 {
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

	// Fallback: try to show the root directly
	output, err := a.run("show", rootID, "--json")
	if err != nil {
		return runner.Issue{}, err
	}
	var fallback []runner.Issue
	if err := traceJSONParse("TreeFallback", []byte(output), &fallback); err != nil {
		return runner.Issue{}, err
	}
	if len(fallback) == 0 {
		return runner.Issue{}, nil
	}
	return fallback[0], nil
}

// listTree fetches child issues using br ready with parent filter
func (a *RustAdapter) listTree(rootID string) ([]runner.Issue, error) {
	output, err := a.run("ready", "--parent", rootID, "--recursive", "--json")
	if err != nil {
		return nil, err
	}
	var issues []runner.Issue
	if err := traceJSONParse("listTree", []byte(output), &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

// readyFallback handles case when no children are ready
func (a *RustAdapter) readyFallback(rootID string) (runner.Issue, error) {
	output, err := a.run("show", rootID, "--json")
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
	if issue.IssueType == "epic" {
		return runner.Issue{}, nil
	}
	return issue, nil
}

// brShowIssue matches the JSON structure returned by br show
type brShowIssue struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Status             string `json:"status"`
}

// Show returns a single issue by ID
func (a *RustAdapter) Show(id string) (runner.Bead, error) {
	output, err := a.run("show", id, "--json")
	if err != nil {
		return runner.Bead{}, err
	}
	var issues []brShowIssue
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

// UpdateStatus updates the status of an issue
func (a *RustAdapter) UpdateStatus(id string, status string) error {
	_, err := a.run("update", id, "--status", status)
	return err
}

// UpdateStatusWithReason updates status and adds a note with reason
func (a *RustAdapter) UpdateStatusWithReason(id string, status string, reason string) error {
	if err := a.UpdateStatus(id, status); err != nil {
		return err
	}
	sanitized := sanitizeReason(reason)
	if sanitized == "" {
		return nil
	}
	_, err := a.run("update", id, "--notes", sanitized)
	return err
}

// Close closes an issue
func (a *RustAdapter) Close(id string) error {
	_, err := a.run("close", id)
	return err
}

// CloseEligible closes epics that have all children closed
func (a *RustAdapter) CloseEligible() error {
	_, err := a.run("epic", "close-eligible")
	return err
}

// Sync exports database to JSONL for git
// Note: br requires --flush-only flag unlike bd
func (a *RustAdapter) Sync() error {
	_, err := a.run("sync", "--flush-only")
	return err
}

// IsRustAvailable checks if beads_rust is available in the repository
func IsRustAvailable(repoRoot string) bool {
	beadsDir := filepath.Join(repoRoot, ".beads")
	_, err := os.Stat(beadsDir)
	return err == nil
}
