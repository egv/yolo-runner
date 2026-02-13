package linear

import (
	"strings"
	"testing"
)

func TestReconstructPromptContextUsesActivitiesOnly(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionPrompted,
		AgentSession: AgentSession{
			PromptContext: "<issue identifier=\"YR-4QC2\"><title>Activities-only conversation reconstruction</title></issue>",
			Comment: &AgentComment{
				ID:   "comment-current",
				Body: "MUTABLE: current comment should never be included",
			},
		},
		PreviousComments: []AgentComment{
			{
				ID:   "comment-prev-1",
				Body: "MUTABLE: prior comment should never be included",
			},
		},
		AgentActivity: &AgentActivity{
			ID: "activity-current-prompt",
			Content: AgentActivityContent{
				Type: AgentActivityContentTypePrompt,
				Body: "Please include retry handling.",
			},
		},
	}

	priorActivities := []AgentActivity{
		{
			ID: "activity-prev-prompt",
			Content: AgentActivityContent{
				Type: AgentActivityContentTypePrompt,
				Body: "Implement webhook receiver.",
			},
		},
		{
			ID: "activity-prev-thought",
			Content: AgentActivityContent{
				Type: AgentActivityContentTypeThought,
				Body: "Start with a failing test first.",
			},
		},
		{
			ID: "activity-prev-action",
			Content: AgentActivityContent{
				Type:      AgentActivityContentTypeAction,
				Action:    "run",
				Parameter: "go test ./internal/linear",
			},
		},
	}

	context := ReconstructPromptContext(event, priorActivities)

	if !strings.Contains(context, event.AgentSession.PromptContext) {
		t.Fatalf("expected prompt context %q in reconstructed context: %q", event.AgentSession.PromptContext, context)
	}

	if !strings.Contains(context, "Implement webhook receiver.") {
		t.Fatalf("expected prior prompt activity in reconstructed context: %q", context)
	}

	if !strings.Contains(context, "Please include retry handling.") {
		t.Fatalf("expected current prompt activity in reconstructed context: %q", context)
	}

	if strings.Contains(context, "MUTABLE:") {
		t.Fatalf("expected mutable comments to be excluded from reconstructed context: %q", context)
	}
}

func TestReconstructPromptContextReturnsPromptContextWhenNoActivities(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionPrompted,
		AgentSession: AgentSession{
			PromptContext: "<issue identifier=\"YR-4QC2\"><title>Activities-only conversation reconstruction</title></issue>",
		},
	}

	context := ReconstructPromptContext(event, nil)
	if context != event.AgentSession.PromptContext {
		t.Fatalf("expected prompt context only, got %q", context)
	}
}
