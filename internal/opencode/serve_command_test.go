package opencode

import "testing"

func TestBuildServeCommandUsesDefaultBinaryAndLoopbackServeArgs(t *testing.T) {
	command := BuildServeCommand("")
	expected := []string{"opencode", "serve", "--hostname", "127.0.0.1"}

	if len(command) != len(expected) {
		t.Fatalf("expected command %#v, got %#v", expected, command)
	}
	for i, want := range expected {
		if command[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, command[i])
		}
	}

	for _, unexpected := range []string{"--port"} {
		for _, arg := range command {
			if arg == unexpected {
				t.Fatalf("did not expect %q in base serve command %#v", unexpected, command)
			}
		}
	}
}

func TestBuildServeCommandArgsReturnsLoopbackServeArgsOnly(t *testing.T) {
	args := BuildServeCommandArgs()
	expected := []string{"serve", "--hostname", "127.0.0.1"}

	if len(args) != len(expected) {
		t.Fatalf("expected args %#v, got %#v", expected, args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Fatalf("expected arg %q at %d, got %q", want, i, args[i])
		}
	}

	for _, unexpected := range []string{"--port"} {
		for _, arg := range args {
			if arg == unexpected {
				t.Fatalf("did not expect %q in base serve args %#v", unexpected, args)
			}
		}
	}
}
