package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
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
			fixedTrackerWatchPollInterval(time.Hour),
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
			fixedTrackerWatchPollInterval(wantInterval),
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
			fixedTrackerWatchPollInterval(time.Hour),
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
			fixedTrackerWatchPollInterval(time.Hour),
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

func TestRunTrackerWatchPollLoopUsesFreshIntervalProviderForEachWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	providerCalls := 0
	waitIntervals := []time.Duration{}

	err := runTrackerWatchPollLoop(
		ctx,
		false,
		func() time.Duration {
			providerCalls++
			return time.Duration(providerCalls) * time.Second
		},
		func(context.Context) error {
			return nil
		},
		func(error) {},
		3,
		func(_ context.Context, interval time.Duration) error {
			waitIntervals = append(waitIntervals, interval)
			if len(waitIntervals) == 3 {
				cancel()
			}
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if providerCalls != 3 {
		t.Fatalf("expected interval provider to be called before each wait, got %d calls", providerCalls)
	}
	wantIntervals := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second}
	if len(waitIntervals) != len(wantIntervals) {
		t.Fatalf("expected wait intervals %#v, got %#v", wantIntervals, waitIntervals)
	}
	for i, want := range wantIntervals {
		if waitIntervals[i] != want {
			t.Fatalf("expected wait interval %d to be %s, got %s", i, want, waitIntervals[i])
		}
	}
}

func fixedTrackerWatchPollInterval(interval time.Duration) trackerWatchPollIntervalProvider {
	return func() time.Duration {
		return interval
	}
}

func TestDefaultRunTrackerWatchDelegatesToSourceStartrekOnce(t *testing.T) {
	originalRunSourceStartrek := runSourceStartrek
	t.Cleanup(func() {
		runSourceStartrek = originalRunSourceStartrek
	})

	called := false
	var got sourceStartrekCommandConfig
	runSourceStartrek = func(_ context.Context, cfg sourceStartrekCommandConfig) error {
		called = true
		got = cfg
		return nil
	}

	err := defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot:   "/repo",
		profile:    "st-dev",
		once:       false,
		stream:     true,
		eventsPath: "/repo/events.jsonl",
	})
	if err != nil {
		t.Fatalf("defaultRunTrackerWatch() error = %v", err)
	}
	if !called {
		t.Fatalf("expected tracker-watch to delegate to source startrek")
	}
	if got.repoRoot != "/repo" {
		t.Fatalf("repoRoot = %q, want /repo", got.repoRoot)
	}
	if got.profile != "st-dev" {
		t.Fatalf("profile = %q, want st-dev", got.profile)
	}
	if !got.once {
		t.Fatalf("once = false, want true")
	}
	if !got.stream {
		t.Fatalf("stream = false, want true")
	}
	if got.eventsPath != "/repo/events.jsonl" {
		t.Fatalf("eventsPath = %q, want /repo/events.jsonl", got.eventsPath)
	}
}

func TestDefaultRunTrackerWatchResolvesDefaultProfileBeforeDelegating(t *testing.T) {
	originalRunSourceStartrek := runSourceStartrek
	t.Cleanup(func() {
		runSourceStartrek = originalRunSourceStartrek
	})

	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
default_profile: st-default
profiles:
  st-default:
    tracker:
      type: startrek
      startrek:
        endpoint: https://st.example.invalid/v3
        token_env: STARTREK_TEST_TOKEN
        queues:
          - key: VAY
            root: `+repoRoot+`
`)
	t.Setenv("STARTREK_TEST_TOKEN", "token")

	var got sourceStartrekCommandConfig
	runSourceStartrek = func(_ context.Context, cfg sourceStartrekCommandConfig) error {
		got = cfg
		return nil
	}

	if err := defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot: repoRoot,
		stream:   true,
	}); err != nil {
		t.Fatalf("defaultRunTrackerWatch() error = %v", err)
	}
	if got.profile != "st-default" {
		t.Fatalf("delegated profile = %q, want st-default", got.profile)
	}
	if !got.once {
		t.Fatalf("once = false, want true")
	}
}

func TestDefaultRunTrackerWatchRejectsDryRunBeforeDelegating(t *testing.T) {
	originalRunSourceStartrek := runSourceStartrek
	t.Cleanup(func() {
		runSourceStartrek = originalRunSourceStartrek
	})

	called := false
	runSourceStartrek = func(context.Context, sourceStartrekCommandConfig) error {
		called = true
		return nil
	}

	err := defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot: "/repo",
		profile:  "st-dev",
		dryRun:   true,
	})
	if err == nil {
		t.Fatalf("expected dry-run to be rejected")
	}
	if !strings.Contains(err.Error(), "--dry-run is not supported") {
		t.Fatalf("expected dry-run support error, got %q", err.Error())
	}
	if called {
		t.Fatalf("expected dry-run not to delegate to source startrek")
	}
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
