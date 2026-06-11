package arcanum

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRunWorkspaceArcPassesWorkspaceArgsAndReturnsStdout(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	var gotCtx context.Context
	var gotWorkspace string
	var gotName string
	var gotArgs []string

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		gotCtx = ctx
		gotWorkspace = workspace
		gotName = name
		gotArgs = append([]string{}, args...)
		return []byte(`{"ok":true}`), nil, nil
	}

	stdout, err := RunWorkspaceArc(ctx, "/arcadia/workspace", "pr", "list", "--json")
	if err != nil {
		t.Fatalf("RunWorkspaceArc() error = %v", err)
	}
	if string(stdout) != `{"ok":true}` {
		t.Fatalf("RunWorkspaceArc() stdout = %q", stdout)
	}
	if gotCtx != ctx {
		t.Fatal("RunWorkspaceArc() did not pass through context")
	}
	if gotWorkspace != "/arcadia/workspace" {
		t.Fatalf("RunWorkspaceArc() workspace = %q", gotWorkspace)
	}
	if gotName != "arc" {
		t.Fatalf("RunWorkspaceArc() command = %q", gotName)
	}
	if wantArgs := []string{"pr", "list", "--json"}; !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("RunWorkspaceArc() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestRunWorkspaceArcIncludesArgsWorkspaceAndTrimmedStderrOnFailure(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("\n  arc: authentication failed  \n\n"), errors.New("exit status 1")
	}

	_, err := RunWorkspaceArc(context.Background(), "/arcadia/workspace", "pr", "list", "--json")
	if err == nil {
		t.Fatal("RunWorkspaceArc() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc pr list --json",
		"/arcadia/workspace",
		"arc: authentication failed",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("RunWorkspaceArc() error = %q, want substring %q", message, want)
		}
	}
	if strings.Contains(message, "\n  arc: authentication failed") ||
		strings.Contains(message, "authentication failed  \n") {
		t.Fatalf("RunWorkspaceArc() error did not trim stderr: %q", message)
	}
}

type contextKey string
