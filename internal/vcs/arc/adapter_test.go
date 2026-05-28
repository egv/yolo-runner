package arc

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeRunner struct {
	output string
	err    error
	calls  []call
}

type call struct {
	name string
	args []string
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string{}, args...)})
	return f.output, f.err
}

func TestIsDirtyReturnsFalseWhenClean(t *testing.T) {
	runner := &fakeRunner{output: ""}
	adapter := New(runner)

	dirty, err := adapter.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if dirty {
		t.Fatal("expected clean Arcadia root")
	}
	assertCalls(t, runner.calls, call{name: "arc", args: []string{"status", "--short"}})
}

func TestIsDirtyReturnsTrueWhenDirty(t *testing.T) {
	runner := &fakeRunner{output: " M ya.make\n"}
	adapter := New(runner)

	dirty, err := adapter.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !dirty {
		t.Fatal("expected dirty Arcadia root")
	}
	assertCalls(t, runner.calls, call{name: "arc", args: []string{"status", "--short"}})
}

func TestIsDirtyPropagatesStatusError(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	adapter := New(runner)

	_, err := adapter.IsDirty(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	assertCalls(t, runner.calls, call{name: "arc", args: []string{"status", "--short"}})
}

func TestCreateTaskBranchRunsArcCheckoutB(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	branch, err := adapter.CreateTaskBranch(context.Background(), "TASK-123")
	if err != nil {
		t.Fatalf("expected create branch to succeed, got %v", err)
	}
	if branch != "task/TASK-123" {
		t.Fatalf("expected branch task/TASK-123, got %q", branch)
	}
	assertCalls(t, runner.calls, call{name: "arc", args: []string{"checkout", "-b", "task/TASK-123"}})
}

func TestCreateTaskBranchFallsBackToCheckoutExistingBranch(t *testing.T) {
	runner := &branchExistsRunner{}
	adapter := New(runner)

	branch, err := adapter.CreateTaskBranch(context.Background(), "TASK-123")
	if err != nil {
		t.Fatalf("expected fallback checkout to succeed, got %v", err)
	}
	if branch != "task/TASK-123" {
		t.Fatalf("expected branch task/TASK-123, got %q", branch)
	}

	want := []call{
		{name: "arc", args: []string{"checkout", "-b", "task/TASK-123"}},
		{name: "arc", args: []string{"checkout", "task/TASK-123"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls: got %#v want %#v", runner.calls, want)
	}
}

func TestCheckoutRunsArcCheckout(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.Checkout(context.Background(), "task/TASK-123"); err != nil {
		t.Fatalf("expected checkout to succeed, got %v", err)
	}
	assertCalls(t, runner.calls, call{name: "arc", args: []string{"checkout", "task/TASK-123"}})
}

func TestArcCommandAdapterRoutesFlatCommandRunnerCalls(t *testing.T) {
	runner := &flatRunner{output: " M ya.make\n"}
	adapter := New(NewArcCommandAdapter(runner))

	dirty, err := adapter.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !dirty {
		t.Fatal("expected dirty Arcadia root")
	}

	want := [][]string{{"arc", "status", "--short"}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("unexpected calls: got %#v want %#v", runner.calls, want)
	}
}

func assertCalls(t *testing.T, got []call, want call) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if got[0].name != want.name || !reflect.DeepEqual(got[0].args, want.args) {
		t.Fatalf("expected call %v %v, got %v %v", want.name, want.args, got[0].name, got[0].args)
	}
}

type branchExistsRunner struct {
	calls []call
}

func (r *branchExistsRunner) Run(name string, args ...string) (string, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string{}, args...)})
	if len(args) >= 3 && args[0] == "checkout" && args[1] == "-b" {
		return "", errors.New("branch already exists")
	}
	return "", nil
}

type flatRunner struct {
	output string
	err    error
	calls  [][]string
}

func (r *flatRunner) Run(args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{}, args...))
	return r.output, r.err
}
