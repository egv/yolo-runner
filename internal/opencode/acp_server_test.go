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
