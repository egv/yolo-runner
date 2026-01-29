package runner

import "context"

// Task represents a task or issue in a task tracking system
type Task struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Status             string `json:"status"`
	Priority           *int   `json:"priority"`
	IssueType          string `json:"issue_type"`
	Children           []Task `json:"children"`
}

// TaskTracker defines the interface for task tracking systems
type TaskTracker interface {
	// Ready returns the ready tasks under the specified root ID
	Ready(rootID string) (Task, error)

	// Tree returns the task tree for the specified root ID
	Tree(rootID string) (Task, error)

	// Show returns details of a specific task
	Show(id string) (Task, error)

	// UpdateStatus updates the status of a task
	UpdateStatus(id string, status string) error

	// UpdateStatusWithReason updates the status of a task with a reason
	UpdateStatusWithReason(id string, status string, reason string) error

	// Close marks a task as closed
	Close(id string) error

	// CloseEligible closes any eligible parent tasks
	CloseEligible() error

	// Sync synchronizes with the remote task tracking system
	Sync() error
}

// CodingAgent defines the interface for coding agents
type CodingAgent interface {
	// Run executes the coding agent for a specific task
	Run(ctx context.Context, task Task, repoRoot string, model string, options RunOptions) error

	// RunWithContext executes the coding agent with a specific context
	RunWithContext(ctx context.Context, task Task, repoRoot string, model string, options RunOptions) error
}

// RunOptions contains options for running the coding agent
type RunOptions struct {
	ConfigRoot string
	ConfigDir  string
	LogPath    string
}
