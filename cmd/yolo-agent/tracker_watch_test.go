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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
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
		if call <= 3 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"temporary tracker outage"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		w.Header().Set("X-Total-Pages", "1")
		_, _ = w.Write([]byte(`[]`))
		if call >= 9 {
			time.AfterFunc(20*time.Millisecond, cancel)
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
  poll_interval: 100ms
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

func TestDefaultRunTrackerWatchReloadsTrackerAgentConfigPerIteration(t *testing.T) {
	repoRoot := t.TempDir()
	lockPath := filepath.Join(repoRoot, "locks", "tracker-agent.lock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configWithLabels := func(ready string, inProgress string, completed string, blocked string, failed string) trackerAgentConfig {
		cfg := defaultTrackerAgentConfig()
		cfg.PollInterval = 10 * time.Millisecond
		cfg.LockPath = lockPath
		cfg.Labels = trackerAgentLabelNamesConfig{
			Ready:      ready,
			InProgress: inProgress,
			Completed:  completed,
			Blocked:    blocked,
			Failed:     failed,
		}
		return cfg
	}
	configService := &fakeTrackerWatchConfigService{
		configs: []trackerAgentConfig{
			configWithLabels("ready-startup", "running-startup", "done-startup", "blocked-startup", "failed-startup"),
			configWithLabels("ready-v1", "running-v1", "done-v1", "blocked-v1", "failed-v1"),
			configWithLabels("ready-v2", "running-v2", "done-v2", "blocked-v2", "failed-v2"),
		},
	}
	originalConfigService := newTrackerWatchConfigService
	newTrackerWatchConfigService = func() trackerAgentConfigResolver {
		return configService
	}
	t.Cleanup(func() {
		newTrackerWatchConfigService = originalConfigService
	})

	var searchCalls int32
	var capturedMu sync.Mutex
	var capturedTags []string
	startrek := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "OAuth tracker-token" {
			t.Fatalf("expected Startrek OAuth token, got %q", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/issues/_search" {
			t.Fatalf("unexpected Startrek request: %s %s", r.Method, r.URL.String())
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		filter, ok := body["filter"].(map[string]any)
		if !ok {
			t.Fatalf("expected search filter object, got %#v", body["filter"])
		}
		capturedMu.Lock()
		capturedTags = append(capturedTags, strings.TrimSpace(fmt.Sprint(filter["tags"])))
		capturedMu.Unlock()

		call := atomic.AddInt32(&searchCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		w.Header().Set("X-Total-Pages", "1")
		_, _ = w.Write([]byte(`[]`))
		if call >= 12 {
			time.AfterFunc(5*time.Millisecond, cancel)
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
`, startrek.URL, repoRoot))
	t.Setenv("STARTREK_TOKEN", "tracker-token")

	err := defaultRunTrackerWatch(ctx, trackerWatchConfig{
		repoRoot: repoRoot,
		profile:  "startrek-demo",
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected tracker-watch to stop by context cancellation, got %v", err)
	}

	capturedMu.Lock()
	tags := append([]string(nil), capturedTags...)
	capturedMu.Unlock()
	readyTags := make([]string, 0, 2)
	for _, tag := range tags {
		if strings.HasPrefix(tag, "ready-") {
			readyTags = append(readyTags, tag)
		}
	}
	if len(readyTags) < 2 {
		t.Fatalf("expected ready label searches from two iterations, got tags %#v", tags)
	}
	if got, want := readyTags[1], "ready-v2"; got != want {
		t.Fatalf("expected second iteration to use reloaded ready label %q, got %q (all ready labels %#v)", want, got, readyTags)
	}
}

func TestDefaultRunTrackerWatchUsesReloadedPollIntervalForNextWait(t *testing.T) {
	repoRoot := t.TempDir()
	lockPath := filepath.Join(repoRoot, "locks", "tracker-agent.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	configWithInterval := func(interval time.Duration) trackerAgentConfig {
		cfg := defaultTrackerAgentConfig()
		cfg.PollInterval = interval
		cfg.LockPath = lockPath
		cfg.Labels.Ready = "ready"
		return cfg
	}
	configService := &fakeTrackerWatchConfigService{
		configs: []trackerAgentConfig{
			configWithInterval(1 * time.Millisecond),
			configWithInterval(1 * time.Millisecond),
			configWithInterval(1 * time.Millisecond),
			configWithInterval(120 * time.Millisecond),
			configWithInterval(120 * time.Millisecond),
		},
	}
	originalConfigService := newTrackerWatchConfigService
	newTrackerWatchConfigService = func() trackerAgentConfigResolver {
		return configService
	}
	t.Cleanup(func() {
		newTrackerWatchConfigService = originalConfigService
	})

	var readySearchMu sync.Mutex
	var readySearchTimes []time.Time
	startrek := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "OAuth tracker-token" {
			t.Fatalf("expected Startrek OAuth token, got %q", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/issues/_search" {
			t.Fatalf("unexpected Startrek request: %s %s", r.Method, r.URL.String())
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		filter, ok := body["filter"].(map[string]any)
		if !ok {
			t.Fatalf("expected search filter object, got %#v", body["filter"])
		}
		if strings.TrimSpace(fmt.Sprint(filter["tags"])) == "ready" {
			readySearchMu.Lock()
			readySearchTimes = append(readySearchTimes, time.Now())
			readySearchCount := len(readySearchTimes)
			readySearchMu.Unlock()
			if readySearchCount >= 3 {
				time.AfterFunc(5*time.Millisecond, cancel)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Total-Count", "0")
		w.Header().Set("X-Total-Pages", "1")
		_, _ = w.Write([]byte(`[]`))
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
`, startrek.URL, repoRoot))
	t.Setenv("STARTREK_TOKEN", "tracker-token")

	err := defaultRunTrackerWatch(ctx, trackerWatchConfig{
		repoRoot: repoRoot,
		profile:  "startrek-demo",
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected tracker-watch to stop by context cancellation, got %v", err)
	}

	readySearchMu.Lock()
	times := append([]time.Time(nil), readySearchTimes...)
	readySearchMu.Unlock()
	if len(times) < 3 {
		t.Fatalf("expected ready-label searches from three iterations, got %d", len(times))
	}
	secondWait := times[2].Sub(times[1])
	if secondWait < 80*time.Millisecond {
		t.Fatalf("expected second wait to use reloaded poll interval, got gap %s", secondWait)
	}
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

func TestFallbackTrackerWatchPreflightQuestionsUseSummary(t *testing.T) {
	questions := fallbackTrackerWatchPreflightQuestions(contracts.Task{
		ID:    "ADAPTABOT-1",
		Title: "Move bot to Messenger",
	}, preflight.Result{
		Summary: "Acceptance criteria are unclear.",
	})

	if len(questions) != 1 {
		t.Fatalf("expected one fallback question, got %#v", questions)
	}
	if !strings.Contains(questions[0], "Acceptance criteria are unclear.") {
		t.Fatalf("expected fallback question to cite preflight summary, got %q", questions[0])
	}
}

func TestFallbackTrackerWatchPreflightQuestionsUseTaskLanguage(t *testing.T) {
	questions := fallbackTrackerWatchPreflightQuestions(contracts.Task{
		ID:          "ADAPTABOT-1",
		Title:       "Перенести бот в Messenger",
		Description: "Нужно уточнить секреты.",
	}, preflight.Result{})

	if len(questions) != 1 {
		t.Fatalf("expected one fallback question, got %#v", questions)
	}
	if !strings.Contains(questions[0], "Добавьте в задачу недостающие детали") {
		t.Fatalf("expected Russian fallback question, got %q", questions[0])
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

type fakeTrackerWatchConfigService struct {
	mu      sync.Mutex
	configs []trackerAgentConfig
	calls   int
	err     error
}

func (s *fakeTrackerWatchConfigService) ResolveTrackerAgentConfig(string) (trackerAgentConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return trackerAgentConfig{}, s.err
	}
	if len(s.configs) == 0 {
		return trackerAgentConfig{}, errors.New("fake tracker-watch config service has no configs")
	}
	index := s.calls - 1
	if index >= len(s.configs) {
		index = len(s.configs) - 1
	}
	return s.configs[index], nil
}
