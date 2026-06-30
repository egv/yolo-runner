package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestEventsFollowMergesExistingFilesByTimestampAndSince(t *testing.T) {
	eventsDir := filepath.Join(t.TempDir(), "events")
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatalf("mkdir events dir: %v", err)
	}

	writeEventsFollowFixture(t, filepath.Join(eventsDir, "runner-b.jsonl"), []string{
		`{"type":"agent_finished","task_id":"task-newer","ts":"2026-06-13T10:00:03Z"}`,
		`not-json`,
	})
	writeEventsFollowFixture(t, filepath.Join(eventsDir, "runner-a.jsonl"), []string{
		`{"type":"agent_started","task_id":"task-old","ts":"2026-06-13T09:59:59Z"}`,
		`{"type":"agent_started","task_id":"task-mid","ts":"2026-06-13T10:00:01Z"}`,
	})

	var out bytes.Buffer
	err := runEventsFollow(context.Background(), eventsFollowConfig{
		eventsDir: eventsDir,
		since:     time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC),
		once:      true,
		out:       &out,
	})
	if err != nil {
		t.Fatalf("events follow failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two merged events after cutoff, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"task_id":"task-mid"`) {
		t.Fatalf("first merged event should be task-mid, got %q", lines[0])
	}
	if !strings.Contains(lines[1], `"task_id":"task-newer"`) {
		t.Fatalf("second merged event should be task-newer, got %q", lines[1])
	}
}

func TestEventsFollowReadsDefaultSourcehostAndRunnerEventFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close queue: %v", err)
		}
	})

	if err := sourcehost.Run(context.Background(), eventsFollowNoopSource{name: "startrek-st-dev"}, store, sourcehost.Options{
		Once:   true,
		ProcID: "startrek-st-dev-123",
	}); err != nil {
		t.Fatalf("run sourcehost fixture: %v", err)
	}
	runnerSink := defaultRunnerDaemonEventSink("runner-a")
	if runnerSink == nil {
		t.Fatalf("default runner event sink is nil")
	}
	if err := runnerSink.Emit(context.Background(), contracts.Event{
		Type:      contracts.EventTypeAgentStarted,
		Proc:      "runner-a",
		ItemID:    "item-1",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("emit runner fixture event: %v", err)
	}

	var out bytes.Buffer
	err = runEventsFollow(context.Background(), eventsFollowConfig{
		once: true,
		out:  &out,
	})
	if err != nil {
		t.Fatalf("events follow failed: %v", err)
	}

	stream := out.String()
	if !strings.Contains(stream, `"proc":"startrek-st-dev-123"`) {
		t.Fatalf("merged stream missing sourcehost process events: %q", stream)
	}
	if !strings.Contains(stream, `"proc":"runner-a"`) {
		t.Fatalf("merged stream missing runner process events: %q", stream)
	}
}

func TestSourceCommandDefaultEventsPathsUseFollowDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoRoot := t.TempDir()
	eventsDir := filepath.Join(home, ".yolo-runner", "events")

	paths := []string{
		resolveSourceArcPREventsPath(sourceArcPRCommandConfig{repoRoot: repoRoot, profile: "arc-dev"}),
		resolveSourceStartrekEventsPath(sourceStartrekCommandConfig{repoRoot: repoRoot, profile: "st-dev"}),
	}
	for _, path := range paths {
		if filepath.Dir(path) != eventsDir {
			t.Fatalf("source default events path = %q, want under %q", path, eventsDir)
		}
		if strings.Contains(path, filepath.Join(repoRoot, "runner-logs")) {
			t.Fatalf("source default events path should not use repo-local runner-logs: %q", path)
		}
	}

	explicit := filepath.Join(repoRoot, "custom.events.jsonl")
	if got := resolveSourceStartrekEventsPath(sourceStartrekCommandConfig{eventsPath: explicit}); got != explicit {
		t.Fatalf("explicit source events path = %q, want %q", got, explicit)
	}
}

type eventsFollowNoopSource struct {
	name string
}

func (s eventsFollowNoopSource) Name() string {
	return s.name
}

func (eventsFollowNoopSource) Poll(context.Context) ([]sourcehost.Submission, error) {
	return nil, nil
}

func (eventsFollowNoopSource) HandleResult(context.Context, workitem.Item, sourcehost.Result) ([]sourcehost.Submission, error) {
	return nil, nil
}

func writeEventsFollowFixture(t *testing.T, path string, lines []string) {
	t.Helper()

	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
