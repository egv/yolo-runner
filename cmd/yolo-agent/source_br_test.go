package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceCommandDispatchesBR(t *testing.T) {
	origRun := runSourceBR
	t.Cleanup(func() { runSourceBR = origRun })

	var got sourceBRCommandConfig
	runSourceBR = func(_ context.Context, cfg sourceBRCommandConfig) error {
		got = cfg
		return nil
	}

	code := sourceCommand([]string{
		"br",
		"--repo", "/repo",
		"--name", "br-source",
		"--queue", "/queue.db",
		"--preset", "yolo-runner",
		"--root", "yolo-epic",
		"--once",
	})
	if code != 0 {
		t.Fatalf("source br exit code = %d, want 0", code)
	}
	if got.repoRoot != "/repo" || got.sourceName != "br-source" || got.queuePath != "/queue.db" || got.preset != "yolo-runner" || got.rootID != "yolo-epic" || !got.once {
		t.Fatalf("unexpected source br config: %#v", got)
	}
}

func TestSourceCommandUsageIncludesBR(t *testing.T) {
	_, stderrText := captureOutput(t, func() {
		code := sourceCommand(nil)
		if code != 1 {
			t.Fatalf("source command exit code = %d, want 1", code)
		}
	})
	if !strings.Contains(stderrText, "source <arcpr|br|startrek>") {
		t.Fatalf("usage should include br source, got %q", stderrText)
	}
}

func TestSourceBRCommandHelpReturnsZero(t *testing.T) {
	_, stderrText := captureOutput(t, func() {
		code := sourceBRCommand([]string{"--help"})
		if code != 0 {
			t.Fatalf("source br --help exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(stderrText, "Usage of yolo-agent source br") {
		t.Fatalf("expected source br usage, got %q", stderrText)
	}
}

func TestBuildSourceBRRunBundleRequiresBeadsWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	_, err := buildSourceBRRunBundle(context.Background(), sourceBRCommandConfig{
		repoRoot:  repoRoot,
		queuePath: filepath.Join(t.TempDir(), "queue.db"),
		preset:    "yolo-runner",
		once:      true,
	})
	if err == nil {
		t.Fatalf("expected missing .beads to fail")
	}
	if !strings.Contains(err.Error(), "br init") {
		t.Fatalf("expected br init remediation, got %q", err.Error())
	}
}
