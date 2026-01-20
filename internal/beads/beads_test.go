package beads

import (
	"errors"
	"strings"
	"testing"

	"yolo-runner/internal/runner"
)

type fakeRunner struct {
	output string
	err    error
	calls  [][]string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	return f.output, f.err
}

func TestReadyLoadsTree(t *testing.T) {
	payload := `[{"id":"root","issue_type":"epic","status":"open","children":[{"id":"task-1","issue_type":"task","status":"open"}]}]`
	runner := &fakeRunner{output: payload}
	adapter := New(runner)

	issue, err := adapter.Ready("root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "root" || issue.IssueType != "epic" {
		t.Fatalf("unexpected issue: %#v", issue)
	}
	if len(issue.Children) != 1 || issue.Children[0].ID != "task-1" {
		t.Fatalf("unexpected children: %#v", issue.Children)
	}

	assertCall(t, runner.calls, []string{"bd", "ready", "--parent", "root", "--json"})
}

func TestShowLoadsBead(t *testing.T) {
	payload := `[{"id":"task-1","title":"Task","description":"Desc","acceptance_criteria":"Acc","status":"open"}]`
	runner := &fakeRunner{output: payload}
	adapter := New(runner)

	bead, err := adapter.Show("task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bead.ID != "task-1" || bead.Title != "Task" || bead.AcceptanceCriteria != "Acc" {
		t.Fatalf("unexpected bead: %#v", bead)
	}

	assertCall(t, runner.calls, []string{"bd", "show", "task-1", "--json"})
}

func TestUpdateStatusCallsBd(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.UpdateStatus("task-1", "blocked"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"bd", "update", "task-1", "--status", "blocked"})
}

func TestUpdateStatusWithReasonCallsBd(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.UpdateStatusWithReason("task-1", "blocked", "no_output last_output_age=10s"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"bd", "update", "task-1", "--status", "blocked", "--reason", "no_output last_output_age=10s"})
}

func TestCloseCallsBd(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.Close("task-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"bd", "close", "task-1"})
}

func TestSyncCallsBd(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.Sync(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"bd", "sync"})
}

func TestErrorsPropagate(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	adapter := New(runner)

	if _, err := adapter.Ready("root"); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := adapter.Show("task-1"); err == nil {
		t.Fatalf("expected error")
	}
	if err := adapter.UpdateStatus("task-1", "open"); err == nil {
		t.Fatalf("expected error")
	}
	if err := adapter.Close("task-1"); err == nil {
		t.Fatalf("expected error")
	}
	if err := adapter.Sync(); err == nil {
		t.Fatalf("expected error")
	}
}

func assertCall(t *testing.T, calls [][]string, expected []string) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatalf("expected call")
	}
	if strings.Join(calls[0], " ") != strings.Join(expected, " ") {
		t.Fatalf("expected call %v, got %v", expected, calls[0])
	}
}

func TestAdapterSatisfiesRunnerInterface(t *testing.T) {
	var _ runner.BeadsClient = New(&fakeRunner{})
}
