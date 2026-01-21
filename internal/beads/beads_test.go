package beads

import (
	"errors"
	"strings"
	"testing"

	"yolo-runner/internal/runner"
)

type fakeRunner struct {
	output  string
	outputs []string
	err     error
	calls   [][]string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	if len(f.outputs) > 0 {
		output := f.outputs[0]
		f.outputs = f.outputs[1:]
		return output, f.err
	}
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

func TestReadyReturnsAllIssuesForSelection(t *testing.T) {
	payload := `[{"id":"task-1","issue_type":"task","status":"in_progress","priority":1},{"id":"task-2","issue_type":"task","status":"open","priority":2},{"id":"task-3","issue_type":"task","status":"open","priority":0}]`
	fake := &fakeRunner{output: payload}
	adapter := New(fake)

	issue, err := adapter.Ready("root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issue.Children) != 3 {
		t.Fatalf("expected 3 children, got %#v", issue.Children)
	}
	leafID := runner.SelectFirstOpenLeafTaskID(issue)
	if leafID != "task-3" {
		t.Fatalf("expected task-3, got %q", leafID)
	}

	assertCall(t, fake.calls, []string{"bd", "ready", "--parent", "root", "--json"})
}

func TestReadyFallsBackToShowOnLeafOpen(t *testing.T) {
	payloadReady := `[]`
	payloadShow := `[{"id":"task-1","issue_type":"task","status":"open"}]`
	runner := &fakeRunner{outputs: []string{payloadReady, payloadShow}}
	adapter := New(runner)

	issue, err := adapter.Ready("task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "task-1" || issue.IssueType != "task" || issue.Status != "open" {
		t.Fatalf("unexpected issue: %#v", issue)
	}

	if len(runner.calls) != 2 {
		t.Fatalf("expected two calls, got %d", len(runner.calls))
	}
	if strings.Join(runner.calls[0], " ") != "bd ready --parent task-1 --json" {
		t.Fatalf("unexpected first call: %v", runner.calls[0])
	}
	if strings.Join(runner.calls[1], " ") != "bd show task-1 --json" {
		t.Fatalf("unexpected second call: %v", runner.calls[1])
	}
}

func TestReadyReturnsEmptyOnClosedLeaf(t *testing.T) {
	payloadReady := `[]`
	payloadShow := `[{"id":"task-1","issue_type":"task","status":"closed"}]`
	runner := &fakeRunner{outputs: []string{payloadReady, payloadShow}}
	adapter := New(runner)

	issue, err := adapter.Ready("task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "" {
		t.Fatalf("expected empty issue, got %#v", issue)
	}
}

func TestReadyReturnsEmptyOnContainerShow(t *testing.T) {
	payloadReady := `[]`
	payloadShow := `[{"id":"epic-1","issue_type":"epic","status":"open"}]`
	runner := &fakeRunner{outputs: []string{payloadReady, payloadShow}}
	adapter := New(runner)

	issue, err := adapter.Ready("epic-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "" {
		t.Fatalf("expected empty issue, got %#v", issue)
	}
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

	if len(runner.calls) != 2 {
		t.Fatalf("expected two calls, got %d", len(runner.calls))
	}
	assertCall(t, runner.calls[:1], []string{"bd", "update", "task-1", "--status", "blocked"})
	assertCall(t, runner.calls[1:], []string{"bd", "update", "task-1", "--notes", "no_output last_output_age=10s"})
}

func TestUpdateStatusWithReasonSanitizesNotes(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	reason := "first line\nsecond line\r\nthird line"
	if err := adapter.UpdateStatusWithReason("task-1", "blocked", reason); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected two calls, got %d", len(runner.calls))
	}
	assertCall(t, runner.calls[1:], []string{"bd", "update", "task-1", "--notes", "first line; second line; third line"})
}

func TestUpdateStatusWithReasonTruncates(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	long := strings.Repeat("a", 600)
	if err := adapter.UpdateStatusWithReason("task-1", "blocked", long); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected two calls, got %d", len(runner.calls))
	}
	if len(runner.calls[1]) < 5 {
		t.Fatalf("unexpected update call: %v", runner.calls[1])
	}
	if got := runner.calls[1][4]; len([]rune(got)) != 500 {
		t.Fatalf("expected 500 rune notes, got %d", len([]rune(got)))
	}
}

func TestUpdateStatusWithReasonTruncatesRunes(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	reason := strings.Repeat("世", 600)
	if err := adapter.UpdateStatusWithReason("task-1", "blocked", reason); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected two calls, got %d", len(runner.calls))
	}
	got := runner.calls[1][4]
	if len([]rune(got)) != 500 {
		t.Fatalf("expected 500 rune notes, got %d", len([]rune(got)))
	}
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

func TestTreeLoadsIssue(t *testing.T) {
	payload := `[{"id":"root","issue_type":"epic","status":"open","children":[{"id":"task-1","issue_type":"task","status":"open"}]}]`
	runner := &fakeRunner{output: payload}
	adapter := New(runner)

	issue, err := adapter.Tree("root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "root" || issue.IssueType != "epic" {
		t.Fatalf("unexpected issue: %#v", issue)
	}
	if len(issue.Children) != 1 || issue.Children[0].ID != "task-1" {
		t.Fatalf("unexpected children: %#v", issue.Children)
	}

	assertCall(t, runner.calls, []string{"bd", "show", "root", "--json"})
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
	if _, err := adapter.Tree("root"); err == nil {
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
