package opencode

import "testing"

func TestBuildServeCommandArgsReturnsServeCommandOnly(t *testing.T) {
	args := BuildServeCommandArgs()
	expected := []string{"serve"}

	if len(args) != len(expected) {
		t.Fatalf("expected args %#v, got %#v", expected, args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, args[i])
		}
	}

	for _, unexpected := range []string{"--hostname", "--port"} {
		for _, arg := range args {
			if arg == unexpected {
				t.Fatalf("did not expect %q in base serve args %#v", unexpected, args)
			}
		}
	}
}
