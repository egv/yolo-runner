package beads

import "testing"

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

func assertCall(t *testing.T, calls [][]string, expected []string) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatalf("expected call")
	}
	if joinArgs(calls[0]) != joinArgs(expected) {
		t.Fatalf("expected call %v, got %v", expected, calls[0])
	}
}

func joinArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := args[0]
	for _, arg := range args[1:] {
		out += " " + arg
	}
	return out
}
