package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
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

func TestSourceBRCommandManualDebugSmoke(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoRoot := t.TempDir()
	mkdirBeadsWorkspace(t, repoRoot)

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "br.args")
	brPath := filepath.Join(binDir, "br")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$BR_LOG"
case "$*" in
  "--no-daemon ready --parent debug-root --recursive --json")
    printf '%s\n' '[{"id":"task-a","issue_type":"task","status":"open"}]'
    exit 0
    ;;
  "--no-daemon show debug-root --json")
    printf '%s\n' '[{"id":"debug-root","title":"Debug Root","status":"open"}]'
    exit 0
    ;;
  "--no-daemon show task-a --json")
    printf '%s\n' '[{"id":"task-a","title":"Task A","description":"first","status":"open"}]'
    exit 0
    ;;
  "--no-daemon dep list debug-root --json"|"--no-daemon dep list task-a --json")
    printf '%s\n' '[]'
    exit 0
    ;;
esac
printf 'unexpected br args: %s\n' "$*" >&2
exit 2
`
	if err := os.WriteFile(brPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake br: %v", err)
	}
	t.Setenv("BR_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	queuePath := filepath.Join(t.TempDir(), "queue.db")
	eventsPath := filepath.Join(t.TempDir(), "source-br.events.jsonl")
	stdoutText, stderrText := captureOutput(t, func() {
		code := RunMain([]string{
			"source", "br",
			"--repo", repoRoot,
			"--queue", queuePath,
			"--preset", "debug-preset",
			"--root", "debug-root",
			"--name", "local-br",
			"--once",
			"--stream",
			"--events", eventsPath,
		}, func(context.Context, runConfig) error {
			t.Fatalf("legacy run function should not be called for source br")
			return nil
		})
		if code != 0 {
			t.Fatalf("source br smoke exit code = %d, want 0", code)
		}
	})
	if stderrText != "" {
		t.Fatalf("source br smoke stderr = %q, want empty", stderrText)
	}

	assertSourceBRStreamEvents(t, stdoutText, "local-br")
	eventsBytes, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read source br events log: %v", err)
	}
	assertSourceBRStreamEvents(t, string(eventsBytes), "local-br")

	store, err := workqueue.Open(queuePath)
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	defer store.Close()
	claimed, err := store.Claim("runner-a", []string{"debug-preset"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected queued br implement item")
	}
	if claimed.Kind != workitem.KindImplement || claimed.Source != "local-br" || claimed.SourceRef != "task-a" || claimed.Preset != "debug-preset" {
		t.Fatalf("unexpected queued item: %#v", claimed)
	}
	payload, err := workitem.DecodeImplementPayload(claimed.Payload)
	if err != nil {
		t.Fatalf("decode queued implement payload: %v", err)
	}
	if payload.TaskID != "task-a" || payload.Title != "Task A" || payload.Description != "first" {
		t.Fatalf("unexpected queued payload: %#v", payload)
	}
	if got := payload.PromptContext.Metadata["queue_root"]; got != "debug-root" {
		t.Fatalf("payload queue_root metadata = %q, want debug-root", got)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read br log: %v", err)
	}
	for _, expected := range []string{
		"--no-daemon ready --parent debug-root --recursive --json",
		"--no-daemon show debug-root --json",
		"--no-daemon show task-a --json",
		"--no-daemon dep list task-a --json",
	} {
		if !strings.Contains(string(logged), expected) {
			t.Fatalf("br calls missing %q in %q", expected, string(logged))
		}
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

func assertSourceBRStreamEvents(t *testing.T, raw string, sourceName string) {
	t.Helper()
	decoder := contracts.NewEventDecoder(bytes.NewReader([]byte(raw)))
	seen := map[contracts.EventType]bool{}
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode source br event stream %q: %v", raw, err)
		}
		if event.Metadata["source"] != sourceName {
			t.Fatalf("event source metadata = %q, want %q in %#v", event.Metadata["source"], sourceName, event)
		}
		seen[event.Type] = true
	}
	for _, eventType := range []contracts.EventType{
		contracts.EventTypeRunStarted,
		contracts.EventTypeRunnerHeartbeat,
		contracts.EventTypeRunFinished,
	} {
		if !seen[eventType] {
			t.Fatalf("source br event stream missing %s in %q", eventType, raw)
		}
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
