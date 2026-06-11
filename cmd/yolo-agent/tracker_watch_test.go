package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRunTrackerWatchPollLoopHonorsOnceAndContextCancel(t *testing.T) {
	t.Run("once runs exactly one iteration without waiting", func(t *testing.T) {
		calls := 0
		waits := 0

		err := runTrackerWatchPollLoop(
			context.Background(),
			true,
			time.Hour,
			func(context.Context) error {
				calls++
				return nil
			},
			func(error) {},
			3,
			func(context.Context, time.Duration) error {
				waits++
				return nil
			},
		)
		if err != nil {
			t.Fatalf("expected once loop to succeed, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected one iteration, got %d", calls)
		}
		if waits != 0 {
			t.Fatalf("expected once loop not to wait, got %d waits", waits)
		}
	})

	t.Run("interval mode repeats on poll interval and stops on context cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const wantInterval = 25 * time.Millisecond
		calls := 0
		var waits []time.Duration

		err := runTrackerWatchPollLoop(
			ctx,
			false,
			wantInterval,
			func(context.Context) error {
				calls++
				if calls == 3 {
					cancel()
				}
				return nil
			},
			func(error) {},
			3,
			func(_ context.Context, interval time.Duration) error {
				waits = append(waits, interval)
				return nil
			},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected three iterations before cancellation, got %d", calls)
		}
		if len(waits) != 2 {
			t.Fatalf("expected two interval waits, got %d", len(waits))
		}
		for _, got := range waits {
			if got != wantInterval {
				t.Fatalf("expected wait interval %s, got %s", wantInterval, got)
			}
		}
	})
}

func TestRunTrackerWatchPollLoopContinuesAfterIterationErrors(t *testing.T) {
	t.Run("keeps polling after transient iteration errors and resets consecutive failures", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errOne := errors.New("tracker failure one")
		errTwo := errors.New("tracker failure two")
		errThree := errors.New("tracker failure three")
		iterationResults := []error{errOne, errTwo, nil, errThree, nil}
		calls := 0
		waits := 0
		var iterationErrors []error

		err := runTrackerWatchPollLoop(
			ctx,
			false,
			time.Hour,
			func(context.Context) error {
				if calls >= len(iterationResults) {
					cancel()
					return nil
				}
				err := iterationResults[calls]
				calls++
				return err
			},
			func(err error) {
				iterationErrors = append(iterationErrors, err)
			},
			3,
			func(context.Context, time.Duration) error {
				waits++
				return nil
			},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation after transient errors, got %v", err)
		}
		if calls != len(iterationResults) {
			t.Fatalf("expected %d iterations, got %d", len(iterationResults), calls)
		}
		if waits != len(iterationResults) {
			t.Fatalf("expected wait after each non-once iteration, got %d", waits)
		}
		if len(iterationErrors) != 3 {
			t.Fatalf("expected three reported iteration errors, got %d", len(iterationErrors))
		}
		for i, want := range []error{errOne, errTwo, errThree} {
			if !errors.Is(iterationErrors[i], want) {
				t.Fatalf("expected reported error %d to be %v, got %v", i, want, iterationErrors[i])
			}
		}
	})

	t.Run("exits after max consecutive iteration failures", func(t *testing.T) {
		errOne := errors.New("tracker failure one")
		errTwo := errors.New("tracker failure two")
		calls := 0
		waits := 0
		var iterationErrors []error

		err := runTrackerWatchPollLoop(
			context.Background(),
			false,
			time.Hour,
			func(context.Context) error {
				calls++
				if calls == 1 {
					return errOne
				}
				return errTwo
			},
			func(err error) {
				iterationErrors = append(iterationErrors, err)
			},
			2,
			func(context.Context, time.Duration) error {
				waits++
				return nil
			},
		)
		if !errors.Is(err, errTwo) {
			t.Fatalf("expected second consecutive error, got %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected two iterations before cap exit, got %d", calls)
		}
		if waits != 1 {
			t.Fatalf("expected one wait before cap exit, got %d", waits)
		}
		if len(iterationErrors) != 2 {
			t.Fatalf("expected two reported iteration errors, got %d", len(iterationErrors))
		}
		if !errors.Is(iterationErrors[0], errOne) || !errors.Is(iterationErrors[1], errTwo) {
			t.Fatalf("unexpected reported errors: %#v", iterationErrors)
		}
	})
}

func TestDefaultRunTrackerWatchEmitsWarningAndContinuesAfterIterationError(t *testing.T) {
	repoRoot := t.TempDir()
	eventsPath := filepath.Join(repoRoot, "runner-logs", "tracker-watch.events.jsonl")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var searchCalls int32
	startrek := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "OAuth tracker-token" {
			t.Fatalf("expected Startrek OAuth token, got %q", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/issues/_search" {
			t.Fatalf("unexpected Startrek request: %s %s", r.Method, r.URL.String())
		}

		call := atomic.AddInt32(&searchCalls, 1)
		if call == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"temporary tracker outage"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		w.Header().Set("X-Total-Pages", "1")
		_, _ = w.Write([]byte(`[]`))
		if call >= 6 {
			time.AfterFunc(time.Millisecond, cancel)
		}
	}))
	defer startrek.Close()

	writeTrackerConfigYAML(t, repoRoot, fmt.Sprintf(`
agent:
  backend: codex-cli
  model: fake-codex
default_profile: startrek-demo
profiles:
  startrek-demo:
    tracker:
      type: startrek
      startrek:
        endpoint: %q
        token_env: STARTREK_TOKEN
        queues:
          - key: VAY
            root: %q
tracker_agent:
  poll_interval: 10ms
`, startrek.URL, repoRoot))
	t.Setenv("STARTREK_TOKEN", "tracker-token")

	err := defaultRunTrackerWatch(ctx, trackerWatchConfig{
		repoRoot:   repoRoot,
		profile:    "startrek-demo",
		eventsPath: eventsPath,
	})
	if err != nil {
		t.Fatalf("expected tracker-watch to keep running after transient iteration error, got %v", err)
	}

	events := readTrackerWatchEvents(t, eventsPath)
	for _, event := range events {
		if event.Type != contracts.EventTypeRunnerWarning {
			continue
		}
		if !strings.Contains(event.Message, "Category:") || !strings.Contains(event.Message, "Cause:") {
			t.Fatalf("expected classified warning text, got %q", event.Message)
		}
		if !strings.Contains(event.Message, "temporary tracker outage") {
			t.Fatalf("expected warning to include iteration error cause, got %q", event.Message)
		}
		return
	}
	t.Fatalf("expected runner_warning event, got %#v", events)
}

func TestDefaultRunTrackerWatchRejectsHeldLock(t *testing.T) {
	repoRoot := t.TempDir()
	lockPath := filepath.Join(repoRoot, "locks", "tracker-agent.lock")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
tracker_agent:
  lock_path: locks/tracker-agent.lock
`)
	release := holdTrackerWatchLockForTest(t, lockPath)
	defer release()

	err := defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot: repoRoot,
		once:     true,
	})
	if err == nil {
		t.Fatalf("expected held tracker-watch lock to fail")
	}
	if !strings.Contains(err.Error(), "tracker-watch lock is already held") {
		t.Fatalf("expected clear lock-held error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), lockPath) {
		t.Fatalf("expected lock path %q in error, got %q", lockPath, err.Error())
	}
}

func TestTrackerWatchArcMountArgsUseSharedObjectStoreAndSafeDefaults(t *testing.T) {
	args := trackerWatchArcMountArgs(
		"/repo/.yolo-runner/arc-mounts/vay",
		"/repo/.yolo-runner/arc-stores/vay/store",
		"/repo/.yolo-runner/arc-stores/shared-store",
		startrekArcMount{},
	)

	want := []string{
		"arc", "mount",
		"-m", "/repo/.yolo-runner/arc-mounts/vay",
		"-S", "/repo/.yolo-runner/arc-stores/vay/store",
		"--object-store", "/repo/.yolo-runner/arc-stores/shared-store",
		"--ssh-tokens",
		"--allow-other",
		"--inode-cache-size", "100000",
		"--cache-size", "134217728",
	}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected arc mount args:\n got %#v\nwant %#v", args, want)
	}
}

func TestTrackerWatchArcMountArgsAllowMacSpecificOptIns(t *testing.T) {
	noHardlinks := true
	noAutoRehash := true
	overrideLazyCheckout := 0
	inodeCacheSize := 200000
	cacheSize := 268435456

	args := trackerWatchArcMountArgs(
		"/mnt/vay",
		"/store/vay",
		"/store/shared",
		startrekArcMount{
			NoHardlinks:          &noHardlinks,
			NoAutoRehash:         &noAutoRehash,
			OverrideLazyCheckout: &overrideLazyCheckout,
			InodeCacheSize:       &inodeCacheSize,
			CacheSize:            &cacheSize,
		},
	)

	for _, want := range []string{
		"--no-hardlinks",
		"--override-lazy-checkout=0",
		"--no-auto-rehash",
		"200000",
		"268435456",
	} {
		if !containsTrackerWatchArg(args, want) {
			t.Fatalf("expected arc mount args to contain %q, got %#v", want, args)
		}
	}
}

func TestTrackerWatchArcMountPathFallsBackToQueueRoot(t *testing.T) {
	repoRoot := t.TempDir()
	queue := startrekQueueModel{
		Key:  "VAY",
		Root: "arcadia/vay",
		ArcMount: &startrekArcMount{
			Enabled: true,
		},
	}

	got := trackerWatchArcMountPath(repoRoot, queue)
	want := filepath.Join(repoRoot, "arcadia", "vay")
	if got != want {
		t.Fatalf("expected queue root to be used as mount path, got %q want %q", got, want)
	}
}

func containsTrackerWatchArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func readTrackerWatchEvents(t *testing.T, path string) []contracts.Event {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tracker-watch events: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	events := make([]contracts.Event, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event contracts.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode tracker-watch event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}
