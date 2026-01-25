package opencode

import (
	"context"
	"path/filepath"
	"testing"
)

func TestACPHandlerAutoApprovesPermission(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	handler := NewACPHandler("issue-1", logPath, nil)
	decision := handler.HandlePermission(context.Background(), "perm-1", "repo.write")
	if decision != ACPDecisionAllow {
		t.Fatalf("expected allow, got %v", decision)
	}
}
