package opencode

import "testing"

func TestBuildACPArgsIncludesPrintLogsAndCwd(t *testing.T) {
	args := BuildACPArgs("/repo")
	expected := []string{"opencode", "acp", "--print-logs", "--cwd", "/repo"}
	if len(args) != len(expected) {
		t.Fatalf("unexpected args length: %v", args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, args[i])
		}
	}
}

func TestBuildACPArgsWithModel(t *testing.T) {
	args := BuildACPArgsWithModel("/repo", "gpt-4o")
	expected := []string{"opencode", "acp", "--print-logs", "--cwd", "/repo", "--model", "gpt-4o"}
	if len(args) != len(expected) {
		t.Fatalf("unexpected args length: %v", args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, args[i])
		}
	}
}

func TestBuildACPArgsWithEmptyModel(t *testing.T) {
	args := BuildACPArgsWithModel("/repo", "")
	expected := []string{"opencode", "acp", "--print-logs", "--cwd", "/repo"}
	if len(args) != len(expected) {
		t.Fatalf("unexpected args length: %v", args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Fatalf("expected %q at %d, got %q", want, i, args[i])
		}
	}
}
