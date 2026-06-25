package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestRunnerResolvePRCommentStubHandlerIsRegisteredNonIsolatedAndEchoesPayload(t *testing.T) {
	registry := defaultRunnerKindRegistry()
	handler, ok := registry[workitem.KindResolvePRComment]
	if !ok {
		t.Fatalf("defaultRunnerKindRegistry() has no handler for %q", workitem.KindResolvePRComment)
	}
	if handler == nil {
		t.Fatalf("defaultRunnerKindRegistry() registered a nil handler for %q", workitem.KindResolvePRComment)
	}

	if runnerKindIsolated(workitem.KindResolvePRComment) {
		t.Fatalf("runnerKindIsolated(%q) = true, want false (no isolated workspace; resolve is driven by HandleResult)", workitem.KindResolvePRComment)
	}

	payload, err := json.Marshal(workitem.ResolvePRCommentPayload{
		PRID:      "1234567",
		CommentID: "c-42",
	})
	if err != nil {
		t.Fatalf("marshal resolve-pr-comment payload: %v", err)
	}

	item := workitem.Item{
		ID:      "item-resolve-pr-comment",
		Kind:    workitem.KindResolvePRComment,
		Preset:  "arc",
		Payload: payload,
	}

	result, err := handler(context.Background(), item, envpreset.Workspace{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	// The runner never writes to Arcanum: it echoes the item payload back so
	// HandleResult can decode it and perform the resolve. The result must carry
	// the payload verbatim, not a synthesized stub summary.
	if string(result.Payload) != string(item.Payload) {
		t.Fatalf("handler result payload = %s, want echo of item payload %s", result.Payload, item.Payload)
	}
}
