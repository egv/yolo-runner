package arcanum

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestShipArcanumClientShipsPRWithArcMergeNow(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	ctx := context.WithValue(context.Background(), contextKey("ship"), "value")
	var gotCtx context.Context
	var gotWorkspace string
	var gotName string
	var gotArgs []string

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		gotCtx = ctx
		gotWorkspace = workspace
		gotName = name
		gotArgs = append([]string{}, args...)
		return nil, nil, nil
	}

	client := NewShipArcanumClient("/arcadia/workspace")
	if err := client.Ship(ctx, "2293787"); err != nil {
		t.Fatalf("Ship() error = %v", err)
	}

	if gotCtx != ctx {
		t.Fatal("Ship() did not pass through context")
	}
	if gotWorkspace != "/arcadia/workspace" {
		t.Fatalf("Ship() workspace = %q", gotWorkspace)
	}
	if gotName != "arc" {
		t.Fatalf("Ship() command = %q", gotName)
	}
	if wantArgs := []string{"pr", "merge", "--now", "2293787"}; !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("Ship() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestShipArcanumClientSurfacesArcErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("requirements are not satisfied"), errors.New("exit status 1")
	}

	client := NewShipArcanumClient("/arcadia/workspace")
	err := client.Ship(context.Background(), "2293787")
	if err == nil {
		t.Fatal("Ship() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc pr merge --now 2293787",
		"/arcadia/workspace",
		"requirements are not satisfied",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("Ship() error = %q, want substring %q", message, want)
		}
	}
}
