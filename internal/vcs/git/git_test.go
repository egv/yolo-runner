package git

import (
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

	dirty, err := adapter.IsDirty()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if dirty {
		t.Fatalf("expected clean repo")
	}
	assertCalls(t, runner.calls, call{name: "git", args: []string{"status", "--porcelain"}})
}

func TestIsDirtyReturnsTrueWhenDirty(t *testing.T) {
	runner := &fakeRunner{output: " M file.go"}
	adapter := New(runner)

	dirty, err := adapter.IsDirty()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !dirty {
		t.Fatalf("expected dirty repo")
	}
	assertCalls(t, runner.calls, call{name: "git", args: []string{"status", "--porcelain"}})
}

func TestIsDirtyPropagatesError(t *testing.T) {
	runner := &fakeRunner{err: errors.New("boom")}
	adapter := New(runner)

	_, err := adapter.IsDirty()
	if err == nil {
		t.Fatalf("expected error")
	}
	assertCalls(t, runner.calls, call{name: "git", args: []string{"status", "--porcelain"}})
}

func TestAddAllRunsGitAdd(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.AddAll(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertCalls(t, runner.calls, call{name: "git", args: []string{"add", "."}})
}

func TestCommitRunsGitCommit(t *testing.T) {
	runner := &fakeRunner{}
	adapter := New(runner)

	if err := adapter.Commit("message"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	assertCalls(t, runner.calls, call{name: "git", args: []string{"commit", "-m", "message"}})
}

func TestRevParseHeadRunsGitRevParse(t *testing.T) {
	runner := &fakeRunner{output: "abc123"}
	adapter := New(runner)

	rev, err := adapter.RevParseHead()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if rev != "abc123" {
		t.Fatalf("expected rev abc123, got %q", rev)
	}
	assertCalls(t, runner.calls, call{name: "git", args: []string{"rev-parse", "HEAD"}})
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
