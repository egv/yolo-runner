package main

import (
	"context"
	"reflect"
	"testing"
)

func TestRunMainRoutesConfigValidateSubcommand(t *testing.T) {
	originalValidate := runConfigValidateMain
	originalInit := runConfigInitMain
	defer func() {
		runConfigValidateMain = originalValidate
		runConfigInitMain = originalInit
	}()

	called := false
	runConfigValidateMain = func(args []string) int {
		called = true
		want := []string{"--repo", "/tmp/repo"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected validate args: got=%v want=%v", args, want)
		}
		return 17
	}
	runConfigInitMain = func([]string) int {
		t.Fatalf("did not expect init handler")
		return 1
	}

	runCalled := false
	run := func(context.Context, runConfig) error {
		runCalled = true
		return nil
	}

	code := RunMain([]string{"config", "validate", "--repo", "/tmp/repo"}, run)
	if code != 17 {
		t.Fatalf("expected validate handler exit code, got %d", code)
	}
	if !called {
		t.Fatalf("expected validate handler to be called")
	}
	if runCalled {
		t.Fatalf("legacy run path should not execute for config validate")
	}
}

func TestRunMainRoutesConfigInitSubcommand(t *testing.T) {
	originalValidate := runConfigValidateMain
	originalInit := runConfigInitMain
	defer func() {
		runConfigValidateMain = originalValidate
		runConfigInitMain = originalInit
	}()

	runConfigValidateMain = func([]string) int {
		t.Fatalf("did not expect validate handler")
		return 1
	}
	called := false
	runConfigInitMain = func(args []string) int {
		called = true
		want := []string{"--repo", "/tmp/repo", "--force"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected init args: got=%v want=%v", args, want)
		}
		return 23
	}

	runCalled := false
	run := func(context.Context, runConfig) error {
		runCalled = true
		return nil
	}

	code := RunMain([]string{"config", "init", "--repo", "/tmp/repo", "--force"}, run)
	if code != 23 {
		t.Fatalf("expected init handler exit code, got %d", code)
	}
	if !called {
		t.Fatalf("expected init handler to be called")
	}
	if runCalled {
		t.Fatalf("legacy run path should not execute for config init")
	}
}

func TestRunMainRoutesConfigSubcommandAfterGlobalFlags(t *testing.T) {
	originalValidate := runConfigValidateMain
	originalInit := runConfigInitMain
	defer func() {
		runConfigValidateMain = originalValidate
		runConfigInitMain = originalInit
	}()

	called := false
	runConfigValidateMain = func(args []string) int {
		called = true
		want := []string{"--repo", "/tmp/repo", "--json"}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected validate args: got=%v want=%v", args, want)
		}
		return 29
	}
	runConfigInitMain = func([]string) int {
		t.Fatalf("did not expect init handler")
		return 1
	}

	runCalled := false
	run := func(context.Context, runConfig) error {
		runCalled = true
		return nil
	}

	code := RunMain([]string{"--repo", "/tmp/repo", "config", "validate", "--json"}, run)
	if code != 29 {
		t.Fatalf("expected validate handler exit code, got %d", code)
	}
	if !called {
		t.Fatalf("expected validate handler to be called")
	}
	if runCalled {
		t.Fatalf("legacy run path should not execute when config follows global flags")
	}
}

func TestRunMainConfigUnknownSubcommandReturnsError(t *testing.T) {
	runCalled := false
	run := func(context.Context, runConfig) error {
		runCalled = true
		return nil
	}

	code := RunMain([]string{"config", "unknown"}, run)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if runCalled {
		t.Fatalf("legacy run path should not execute for unknown config command")
	}
}

func TestRunMainLegacyFlagsStillUseRunPath(t *testing.T) {
	originalValidate := runConfigValidateMain
	originalInit := runConfigInitMain
	defer func() {
		runConfigValidateMain = originalValidate
		runConfigInitMain = originalInit
	}()

	runConfigValidateMain = func([]string) int {
		t.Fatalf("did not expect validate handler")
		return 1
	}
	runConfigInitMain = func([]string) int {
		t.Fatalf("did not expect init handler")
		return 1
	}

	repoRoot := t.TempDir()
	runCalled := false
	run := func(_ context.Context, cfg runConfig) error {
		runCalled = true
		if cfg.repoRoot != repoRoot {
			t.Fatalf("expected repo root %q, got %q", repoRoot, cfg.repoRoot)
		}
		if cfg.rootID != "root-1" {
			t.Fatalf("expected root id root-1, got %q", cfg.rootID)
		}
		return nil
	}

	code := RunMain([]string{"--repo", repoRoot, "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !runCalled {
		t.Fatalf("expected legacy run path to execute")
	}
}
