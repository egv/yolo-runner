package tk

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/internal/contracts"
)

func TestGitStatePersisterCommitsStatusChanges(t *testing.T) {
	r := &persistenceRunner{outputs: map[string]string{
		"git status --short -- .tickets/t-1.md": "M  .tickets/t-1.md\n",
	}}
	p := NewGitStatePersister(r)

	if err := p.PersistTaskStatusChange(context.Background(), "t-1", contracts.TaskStatusClosed); err != nil {
		t.Fatalf("PersistTaskStatusChange failed: %v", err)
	}

	want := []string{
		"git add -- .tickets/t-1.md",
		"git status --short -- .tickets/t-1.md",
		"git commit -m chore(tickets): persist t-1 status closed -- .tickets/t-1.md",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("unexpected call sequence:\n got: %#v\nwant: %#v", r.calls, want)
	}
}

func TestGitStatePersisterSkipsCommitWhenTicketFileUnchanged(t *testing.T) {
	r := &persistenceRunner{outputs: map[string]string{
		"git status --short -- .tickets/t-1.md": "",
	}}
	p := NewGitStatePersister(r)

	if err := p.PersistTaskDataChange(context.Background(), "t-1", map[string]string{"triage_status": "blocked"}); err != nil {
		t.Fatalf("PersistTaskDataChange failed: %v", err)
	}

	want := []string{
		"git add -- .tickets/t-1.md",
		"git status --short -- .tickets/t-1.md",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("unexpected call sequence:\n got: %#v\nwant: %#v", r.calls, want)
	}
}

func TestGitStatePersisterReturnsCommitErrors(t *testing.T) {
	r := &persistenceRunner{
		outputs: map[string]string{
			"git status --short -- .tickets/t-1.md": "M  .tickets/t-1.md\n",
		},
		errors: map[string]error{
			"git commit -m chore(tickets): persist t-1 status in_progress -- .tickets/t-1.md": errors.New("exit status 1"),
		},
	}
	p := NewGitStatePersister(r)

	err := p.PersistTaskStatusChange(context.Background(), "t-1", contracts.TaskStatusInProgress)
	if err == nil {
		t.Fatalf("expected commit failure")
	}
	if !strings.Contains(err.Error(), "commit ticket state") {
		t.Fatalf("expected commit context in error, got %q", err.Error())
	}
}

type persistenceRunner struct {
	outputs map[string]string
	errors  map[string]error
	calls   []string
}

func (r *persistenceRunner) Run(args ...string) (string, error) {
	joined := strings.Join(args, " ")
	r.calls = append(r.calls, joined)
	if output, ok := r.outputs[joined]; ok {
		return output, r.errors[joined]
	}
	if err, ok := r.errors[joined]; ok {
		return "", err
	}
	return "", nil
}
