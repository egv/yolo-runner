package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
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

func TestBuildSourceBRRunBundleWithoutRootPollsWorkspaceReadyIssues(t *testing.T) {
	repoRoot := t.TempDir()
	mkdirBeadsWorkspace(t, repoRoot)

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "br.args")
	brPath := filepath.Join(binDir, "br")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$BR_LOG"
if [ "$1" = "--no-daemon" ] && [ "$2" = "ready" ] && [ "$3" = "--limit" ] && [ "$4" = "0" ] && [ "$5" = "--json" ]; then
  printf '%s\n' '[{"id":"task-a","title":"Task A","description":"first","issue_type":"task","status":"open","priority":4}]'
  exit 0
fi
printf 'unexpected br args: %s\n' "$*" >&2
exit 2
`
	if err := os.WriteFile(brPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake br: %v", err)
	}
	t.Setenv("BR_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	queuePath := filepath.Join(t.TempDir(), "queue.db")
	bundle, err := buildSourceBRRunBundle(context.Background(), sourceBRCommandConfig{
		repoRoot:   repoRoot,
		sourceName: "br-source",
		queuePath:  queuePath,
		preset:     "yolo-runner",
		once:       true,
	})
	if err != nil {
		t.Fatalf("buildSourceBRRunBundle() error = %v", err)
	}
	defer bundle.Close()

	firstSubmissions, err := bundle.Source.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll(first) error = %v", err)
	}
	secondSubmissions, err := bundle.Source.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll(second) error = %v", err)
	}
	if got, want := len(firstSubmissions), 1; got != want {
		t.Fatalf("Poll(first) submissions = %d, want %d", got, want)
	}
	if got, want := len(secondSubmissions), 0; got != want {
		t.Fatalf("Poll(second) submissions = %d, want %d", got, want)
	}

	claimed, err := bundle.Store.Claim("runner-a", []string{"yolo-runner"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("Claim(first) = nil, want task-a")
	}
	if claimed.Kind != workitem.KindImplement || claimed.Source != "br-source" || claimed.SourceRef != "task-a" || claimed.Preset != "yolo-runner" || claimed.Priority != 4 {
		t.Fatalf("unexpected queued item: %#v", claimed)
	}
	if claimed.IdempotencyKey != "br-source/task-a/implement" {
		t.Fatalf("idempotency key = %q, want rootless stable key", claimed.IdempotencyKey)
	}

	duplicate, err := bundle.Store.Claim("runner-b", []string{"yolo-runner"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(second) error = %v", err)
	}
	if duplicate != nil {
		t.Fatalf("Claim(second) = %#v, want no duplicate", duplicate)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read br log: %v", err)
	}
	if got := strings.TrimSpace(string(logged)); got != strings.Join([]string{
		"--no-daemon ready --limit 0 --json",
		"--no-daemon ready --limit 0 --json",
	}, "\n") {
		t.Fatalf("br calls = %q, want two workspace-wide ready polls", got)
	}
}
