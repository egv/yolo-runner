package distributed

import "testing"

func TestTaskRewriteModelPolicySelectsLargerModel(t *testing.T) {
	policy := TaskRewriteModelPolicy{
		DefaultModel: "gpt-5.3-codex",
		LargerModel:  "gpt-5.3-codex-large",
	}

	selected, reason := policy.Select(ServiceRequestPayload{
		Service: ServiceNameTaskRewrite,
		Metadata: map[string]string{
			"rewrite_policy": "larger_model",
		},
	})
	if selected != "gpt-5.3-codex-large" {
		t.Fatalf("expected larger model, got %q", selected)
	}
	if reason != "policy:larger_model" {
		t.Fatalf("expected larger_model reason, got %q", reason)
	}
}

func TestTaskRewriteModelPolicyUsesRequestedModelOverride(t *testing.T) {
	policy := TaskRewriteModelPolicy{
		DefaultModel: "gpt-5.3-codex",
		LargerModel:  "gpt-5.3-codex-large",
	}

	selected, reason := policy.Select(ServiceRequestPayload{
		Service: ServiceNameTaskRewrite,
		Metadata: map[string]string{
			"model": "custom-rewrite-model",
		},
	})
	if selected != "custom-rewrite-model" {
		t.Fatalf("expected metadata override model, got %q", selected)
	}
	if reason != "metadata:model" {
		t.Fatalf("expected metadata selection reason, got %q", reason)
	}
}
