package runner

import (
	"context"
	"strings"
)

// TaskTrackerAdapter adapts TaskTracker to BeadsClient interface
type TaskTrackerAdapter struct {
	taskTracker TaskTracker
}

func NewTaskTrackerAdapter(tt TaskTracker) *TaskTrackerAdapter {
	return &TaskTrackerAdapter{
		taskTracker: tt,
	}
}

func (t *TaskTrackerAdapter) Ready(rootID string) (Issue, error) {
	task, err := t.taskTracker.Ready(rootID)
	if err != nil {
		return Issue{}, err
	}
	return convertTaskToIssue(task), nil
}

func (t *TaskTrackerAdapter) Tree(rootID string) (Issue, error) {
	task, err := t.taskTracker.Tree(rootID)
	if err != nil {
		return Issue{}, err
	}
	return convertTaskToIssue(task), nil
}

func (t *TaskTrackerAdapter) Show(id string) (Bead, error) {
	task, err := t.taskTracker.Show(id)
	if err != nil {
		return Bead{}, err
	}
	return convertTaskToBead(task), nil
}

func (t *TaskTrackerAdapter) UpdateStatus(id string, status string) error {
	return t.taskTracker.UpdateStatus(id, status)
}

func (t *TaskTrackerAdapter) UpdateStatusWithReason(id string, status string, reason string) error {
	return t.taskTracker.UpdateStatusWithReason(id, status, reason)
}

func (t *TaskTrackerAdapter) Close(id string) error {
	return t.taskTracker.Close(id)
}

func (t *TaskTrackerAdapter) CloseEligible() error {
	return t.taskTracker.CloseEligible()
}

func (t *TaskTrackerAdapter) Sync() error {
	return t.taskTracker.Sync()
}

// CodingAgentAdapter adapts CodingAgent to OpenCodeRunner interface
type CodingAgentAdapter struct {
	codingAgent CodingAgent
	prompt      string
}

func NewCodingAgentAdapter(ca CodingAgent) *CodingAgentAdapter {
	return &CodingAgentAdapter{
		codingAgent: ca,
	}
}

func (c *CodingAgentAdapter) Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	// Convert the issue ID and prompt to a Task for the new interface
	task := Task{
		ID:    issueID,
		Title: extractTitleFromPrompt(prompt),
	}

	options := RunOptions{
		ConfigRoot: configRoot,
		ConfigDir:  configDir,
		LogPath:    logPath,
	}

	ctx := context.Background()
	return c.codingAgent.Run(ctx, task, repoRoot, model, options)
}

func (c *CodingAgentAdapter) RunWithContext(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	// Convert the issue ID and prompt to a Task for the new interface
	task := Task{
		ID:    issueID,
		Title: extractTitleFromPrompt(prompt),
	}

	options := RunOptions{
		ConfigRoot: configRoot,
		ConfigDir:  configDir,
		LogPath:    logPath,
	}

	return c.codingAgent.RunWithContext(ctx, task, repoRoot, model, options)
}

// Helper functions to convert between types
func convertTaskToIssue(task Task) Issue {
	issue := Issue{
		ID:        task.ID,
		IssueType: task.IssueType,
		Status:    task.Status,
		Priority:  task.Priority,
	}

	if len(task.Children) > 0 {
		issue.Children = make([]Issue, len(task.Children))
		for i, child := range task.Children {
			issue.Children[i] = convertTaskToIssue(child)
		}
	}

	return issue
}

func convertTaskToBead(task Task) Bead {
	return Bead{
		ID:                 task.ID,
		Title:              task.Title,
		Description:        task.Description,
		AcceptanceCriteria: task.AcceptanceCriteria,
		Status:             task.Status,
	}
}

func extractTitleFromPrompt(prompt string) string {
	// Simple extraction - in real implementation this might be more sophisticated
	// For now, just take the first line or first 50 characters
	lines := strings.Split(prompt, "\n")
	if len(lines) > 0 && len(lines[0]) > 0 {
		if len(lines[0]) > 50 {
			return lines[0][:50] + "..."
		}
		return lines[0]
	}
	if len(prompt) > 50 {
		return prompt[:50] + "..."
	}
	return prompt
}
