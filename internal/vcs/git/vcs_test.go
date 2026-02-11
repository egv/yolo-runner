package git

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestVCSAdapterImplementsContract(t *testing.T) {
	var _ contracts.VCS = (*VCSAdapter)(nil)
}

func TestEnsureMainChecksOutAndFastForwardsMain(t *testing.T) {
	r := &fakeRunner{}
	a := NewVCSAdapter(r)

	if err := a.EnsureMain(context.Background()); err != nil {
		t.Fatalf("ensure main failed: %v", err)
	}

	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[0], call{name: "git", args: []string{"checkout", "main"}}) {
		t.Fatalf("unexpected call[0]: %#v", r.calls[0])
	}
	if !reflect.DeepEqual(r.calls[1], call{name: "git", args: []string{"pull", "--ff-only", "origin", "main"}}) {
		t.Fatalf("unexpected call[1]: %#v", r.calls[1])
	}
}

func TestEnsureMainIncludesGitOutputInCheckoutFailure(t *testing.T) {
	r := &fakeRunner{
		output: "error: Your local changes to the following files would be overwritten by checkout",
		err:    errors.New("exit status 1"),
	}
	a := NewVCSAdapter(r)

	err := a.EnsureMain(context.Background())
	if err == nil {
		t.Fatal("expected checkout error")
	}
	if !contains(err.Error(), "git checkout main failed") {
		t.Fatalf("expected command context in error, got %q", err.Error())
	}
	if !contains(err.Error(), "Your local changes") {
		t.Fatalf("expected git stderr details in error, got %q", err.Error())
	}
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

	if len(r.calls) != 3 {
		t.Fatalf("expected 3 git calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[0], call{name: "git", args: []string{"checkout", "main"}}) {
		t.Fatalf("unexpected first call: %#v", r.calls[0])
	}
	if !reflect.DeepEqual(r.calls[1], call{name: "git", args: []string{"pull", "--ff-only", "origin", "main"}}) {
		t.Fatalf("unexpected second call: %#v", r.calls[1])
	}
	if !reflect.DeepEqual(r.calls[2], call{name: "git", args: []string{"checkout", "-b", "task/task-123"}}) {
		t.Fatalf("unexpected third call: %#v", r.calls[2])
	}
}

func TestCreateTaskBranchFallsBackToCheckoutExistingBranch(t *testing.T) {
	r := &branchExistsRunner{}
	a := NewVCSAdapter(r)

	branch, err := a.CreateTaskBranch(context.Background(), "task-123")
	if err != nil {
		t.Fatalf("expected fallback checkout to succeed: %v", err)
	}
	if branch != "task/task-123" {
		t.Fatalf("unexpected branch %q", branch)
	}

	if len(r.calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[3], call{name: "git", args: []string{"checkout", "task/task-123"}}) {
		t.Fatalf("expected fallback checkout, got %#v", r.calls[3])
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

	if len(r.calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(r.calls))
	}
	if !reflect.DeepEqual(r.calls[0], call{name: "git", args: []string{"checkout", "main"}}) {
		t.Fatalf("unexpected call[0]: %#v", r.calls[0])
	}
	if !reflect.DeepEqual(r.calls[1], call{name: "git", args: []string{"pull", "--ff-only", "origin", "main"}}) {
		t.Fatalf("unexpected call[1]: %#v", r.calls[1])
	}
	if !reflect.DeepEqual(r.calls[2], call{name: "git", args: []string{"merge", "--no-ff", "task/task-123"}}) {
		t.Fatalf("unexpected call[2]: %#v", r.calls[2])
	}
	if !reflect.DeepEqual(r.calls[3], call{name: "git", args: []string{"push", "origin", "main"}}) {
		t.Fatalf("unexpected call[3]: %#v", r.calls[3])
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

type branchExistsRunner struct {
	calls []call
}

func contains(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func (r *branchExistsRunner) Run(name string, args ...string) (string, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string{}, args...)})
	if len(args) >= 3 && args[0] == "checkout" && args[1] == "-b" {
		return "", errors.New("fatal: a branch named already exists")
	}
	return "", nil
}
