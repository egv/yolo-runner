package tk

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/internal/contracts"
)

type taskStatePersister interface {
	PersistTaskStatusChange(ctx context.Context, taskID string, status contracts.TaskStatus) error
	PersistTaskDataChange(ctx context.Context, taskID string, data map[string]string) error
}

type noopTaskStatePersister struct{}

func (noopTaskStatePersister) PersistTaskStatusChange(context.Context, string, contracts.TaskStatus) error {
	return nil
}

func (noopTaskStatePersister) PersistTaskDataChange(context.Context, string, map[string]string) error {
	return nil
}

type GitStatePersister struct {
	runner Runner
	mu     sync.Mutex
}

func NewGitStatePersister(runner Runner) *GitStatePersister {
	return &GitStatePersister{runner: runner}
}

func (p *GitStatePersister) PersistTaskStatusChange(_ context.Context, taskID string, status contracts.TaskStatus) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	return p.persistTicketFile(taskID, fmt.Sprintf("chore(tickets): persist %s status %s", taskID, status))
}

func (p *GitStatePersister) PersistTaskDataChange(_ context.Context, taskID string, _ map[string]string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	return p.persistTicketFile(taskID, fmt.Sprintf("chore(tickets): persist %s metadata update", taskID))
}

func (p *GitStatePersister) persistTicketFile(taskID string, message string) error {
	if p == nil || p.runner == nil {
		return fmt.Errorf("git state persister is not initialized")
	}

	ticketPath := filepath.ToSlash(filepath.Join(".tickets", taskID+".md"))
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.runner.Run("git", "add", "--", ticketPath); err != nil {
		return fmt.Errorf("stage ticket state %q: %w", ticketPath, err)
	}

	statusOutput, err := p.runner.Run("git", "status", "--short", "--", ticketPath)
	if err != nil {
		return fmt.Errorf("inspect ticket state %q: %w", ticketPath, err)
	}
	if strings.TrimSpace(statusOutput) == "" {
		return nil
	}

	commitOutput, err := p.runner.Run("git", "commit", "-m", message, "--", ticketPath)
	if err != nil {
		if isNoChangesCommitOutput(commitOutput) {
			return nil
		}
		return fmt.Errorf("commit ticket state %q: %w", ticketPath, err)
	}
	return nil
}

func isNoChangesCommitOutput(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "no changes added to commit")
}
