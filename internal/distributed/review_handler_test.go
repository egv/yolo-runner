package distributed

import "testing"

func TestReviewModelPolicySelectsLargerModel(t *testing.T) {
	policy := ReviewModelPolicy{
		DefaultModel: "gpt-5.3-codex",
		LargerModel:  "gpt-5.3-codex-large",
	}

	selected, reason := policy.Select(ServiceRequestPayload{
		Service: ServiceNameReview,
		Metadata: map[string]string{
			"review_policy": "larger_model",
		},
	})
	if selected != "gpt-5.3-codex-large" {
		t.Fatalf("expected larger model, got %q", selected)
	}
	if reason != "policy:larger_model" {
		t.Fatalf("expected larger_model reason, got %q", reason)
	}
}

func TestReviewModelPolicyUsesRequestedModelOverride(t *testing.T) {
	policy := ReviewModelPolicy{
		DefaultModel: "gpt-5.3-codex",
		LargerModel:  "gpt-5.3-codex-large",
	}

	selected, reason := policy.Select(ServiceRequestPayload{
		Service: ServiceNameReview,
		Metadata: map[string]string{
			"model": "custom-review-model",
		},
	})
	if selected != "custom-review-model" {
		t.Fatalf("expected metadata override model, got %q", selected)
	}
	if reason != "metadata:model" {
		t.Fatalf("expected metadata selection reason, got %q", reason)
	}
}
