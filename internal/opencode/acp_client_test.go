package opencode

import (
	"context"
	"testing"
)

func TestACPHandlerAutoApprovesPermission(t *testing.T) {
	handler := NewACPHandler("issue-1", "/tmp/log.jsonl", nil)
	decision := handler.HandlePermission(context.Background(), "perm-1", "repo.write")
	if decision != ACPDecisionAllow {
		t.Fatalf("expected allow, got %v", decision)
	}
}
