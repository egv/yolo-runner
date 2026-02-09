package git

import (
	"context"
	"reflect"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestVCSAdapterImplementsContract(t *testing.T) {
	var _ contracts.VCS = (*VCSAdapter)(nil)
}

func TestEnsureMainChecksOutMain(t *testing.T) {
	r := &fakeRunner{}
	a := NewVCSAdapter(r)

	if err := a.EnsureMain(context.Background()); err != nil {
		t.Fatalf("ensure main failed: %v", err)
	}

	assertVCSCall(t, r.calls, call{name: "git", args: []string{"checkout", "main"}})
}

func TestCreateTaskBranchFromMain(t *testing.T) {
	r := &fakeRunner{}
	a := NewVCSAdapter(r)

	branch, err := a.CreateTaskBranch(context.Background(), "task-123")
	if err != nil {
		t.Fatalf("create task branch failed: %v", err)
	}
	if branch != "task/task-123" {
		t.Fatalf("unexpected branch name: %q", branch)
	}

	if len(r.calls) != 2 {
		t.Fatalf("expected 2 git calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[0], call{name: "git", args: []string{"checkout", "main"}}) {
		t.Fatalf("unexpected first call: %#v", r.calls[0])
	}
	if !reflect.DeepEqual(r.calls[1], call{name: "git", args: []string{"checkout", "-b", "task/task-123"}}) {
		t.Fatalf("unexpected second call: %#v", r.calls[1])
	}
}

func TestMergeAndPushMain(t *testing.T) {
	r := &fakeRunner{}
	a := NewVCSAdapter(r)

	if err := a.MergeToMain(context.Background(), "task/task-123"); err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if err := a.PushMain(context.Background()); err != nil {
		t.Fatalf("push main failed: %v", err)
	}

	if len(r.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[0], call{name: "git", args: []string{"checkout", "main"}}) {
		t.Fatalf("unexpected call[0]: %#v", r.calls[0])
	}
	if !reflect.DeepEqual(r.calls[1], call{name: "git", args: []string{"merge", "--no-ff", "task/task-123"}}) {
		t.Fatalf("unexpected call[1]: %#v", r.calls[1])
	}
	if !reflect.DeepEqual(r.calls[2], call{name: "git", args: []string{"push", "origin", "main"}}) {
		t.Fatalf("unexpected call[2]: %#v", r.calls[2])
	}
}

func TestPushBranch(t *testing.T) {
	r := &fakeRunner{}
	a := NewVCSAdapter(r)

	if err := a.PushBranch(context.Background(), "task/task-123"); err != nil {
		t.Fatalf("push branch failed: %v", err)
	}

	assertVCSCall(t, r.calls, call{name: "git", args: []string{"push", "-u", "origin", "task/task-123"}})
}

func TestCommitAll(t *testing.T) {
	r := &fakeRunner{output: "abc123\n"}
	a := NewVCSAdapter(r)

	sha, err := a.CommitAll(context.Background(), "feat: test")
	if err != nil {
		t.Fatalf("commit all failed: %v", err)
	}
	if sha != "abc123" {
		t.Fatalf("expected sha abc123, got %q", sha)
	}

	if len(r.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[0], call{name: "git", args: []string{"add", "."}}) {
		t.Fatalf("unexpected call[0]: %#v", r.calls[0])
	}
	if !reflect.DeepEqual(r.calls[1], call{name: "git", args: []string{"commit", "-m", "feat: test"}}) {
		t.Fatalf("unexpected call[1]: %#v", r.calls[1])
	}
	if !reflect.DeepEqual(r.calls[2], call{name: "git", args: []string{"rev-parse", "HEAD"}}) {
		t.Fatalf("unexpected call[2]: %#v", r.calls[2])
	}
}

func assertVCSCall(t *testing.T, got []call, want call) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("expected %#v, got %#v", want, got[0])
	}
}
