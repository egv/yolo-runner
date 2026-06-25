package arcpr

import (
	"context"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// fakeResolveApplier is a test double for arcreview.PRReviewCycleResolveApplier,
// used to prove Source.resolveApplier() returns the injected applier by identity
// rather than rebuilding it.
type fakeResolveApplier struct{}

func (f *fakeResolveApplier) Apply(_ context.Context, _ arcreview.PRRuntimeState, _ []byte) (arcreview.ResolveResult, error) {
	return arcreview.ResolveResult{}, nil
}

// TestSourceResolveApplier mirrors the replyApplier()/reviewApplier() contract:
// the injected applier wins when set, otherwise it is built from an explicit
// Arcanum APIClient. With neither injected nor an explicit client, it must error
// per the no-implicit-production-client rule (rather than silently fall back to
// real Arcanum).
func TestSourceResolveApplier(t *testing.T) {
	t.Run("returns injected applier by identity", func(t *testing.T) {
		injected := &fakeResolveApplier{}
		src := &Source{ResolveApplier: injected}

		got, err := src.resolveApplier()
		if err != nil {
			t.Fatalf("resolveApplier() error = %v, want nil when applier injected", err)
		}
		if got != arcreview.PRReviewCycleResolveApplier(injected) {
			t.Fatalf("resolveApplier() = %v, want the injected applier (do not rebuild)", got)
		}
	})

	t.Run("errors when no applier and no explicit API client", func(t *testing.T) {
		src := &Source{} // no ResolveApplier, no APIClient

		got, err := src.resolveApplier()
		if err == nil {
			t.Fatalf("resolveApplier() error = nil, want error (no implicit production Arcanum client)")
		}
		if got != nil {
			t.Fatalf("resolveApplier() = %v, want nil applier on error", got)
		}
	})

	t.Run("builds applier from explicit API client", func(t *testing.T) {
		apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{BaseURL: "https://example.test/api"})
		if err != nil {
			t.Fatalf("NewAPIClient() error = %v", err)
		}
		src := &Source{APIClient: apiClient}

		got, err := src.resolveApplier()
		if err != nil {
			t.Fatalf("resolveApplier() error = %v, want nil when API client is explicit", err)
		}
		if got == nil {
			t.Fatalf("resolveApplier() = nil, want a built applier")
		}
	})
}
