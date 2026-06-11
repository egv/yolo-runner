package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
