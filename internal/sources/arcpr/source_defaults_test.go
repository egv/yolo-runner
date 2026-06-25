package arcpr

import (
	"context"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// TestNewSourceDefaultsAuthorModeGatesEnabled asserts that a freshly constructed
// arcpr Source opts in to every author-mode behavior by default. Because the
// gates are plain bools (zero value false), NewSource is the single place that
// enforces the default-true intent so that production wiring cannot silently
// disable author mode by forgetting to set a flag.
func TestNewSourceDefaultsAuthorModeGatesEnabled(t *testing.T) {
	src := NewSource()
	if src == nil {
		t.Fatalf("NewSource() returned nil")
	}
	if src.Queue != nil {
		t.Fatalf("NewSource() Queue = %v, want nil (wired by cmd, not the constructor)", src.Queue)
	}
	gates := map[string]bool{
		"AuthorModeEnabled":      src.AuthorModeEnabled,
		"AutoArgueEnabled":       src.AutoArgueEnabled,
		"ResolveEnabled":         src.ResolveEnabled,
		"ImplementFanOutEnabled": src.ImplementFanOutEnabled,
	}
	for name, enabled := range gates {
		if !enabled {
			t.Fatalf("NewSource() %s = false, want true (author-mode gates default on)", name)
		}
	}
}

// TestSourcePollIsNilQueueSafe asserts that Poll tolerates an unset Queue
// handle. The Queue is wired by cmd for orchestration and is optional at the
// Source/discovery layer; discovery must never assume it is present.
func TestSourcePollIsNilQueueSafe(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)

	src := NewSource()
	src.SourceName = "arcpr-adapta"
	src.Preset = "adapta"
	src.Reviewer = "alice"
	src.State = state
	src.Lister = PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
		return []arcanum.PRSummary{{ID: "101", FromID: "rev-1", Status: "open"}}, nil
	})
	src.StateFetcher = PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
		return arcreview.PRRuntimeState{PRID: prID, Details: arcreview.PRDetails{ID: prID}}, nil
	})
	// Queue intentionally left nil to prove discovery is Queue-nil-safe.

	subs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v with nil Queue", err)
	}
	if len(subs) != 1 {
		t.Fatalf("Poll() returned %d submissions, want 1 (Queue must be nil-safe): %#v", len(subs), subs)
	}
}
