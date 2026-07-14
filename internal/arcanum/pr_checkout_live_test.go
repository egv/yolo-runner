package arcanum

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPreparePRCheckoutLiveRecovery exercises the real arc CLI against the PR
// named in YOLO_LIVE_CHECKOUT_PR. Guarded so it never runs in CI.
func TestPreparePRCheckoutLiveRecovery(t *testing.T) {
	prID := os.Getenv("YOLO_LIVE_CHECKOUT_PR")
	if prID == "" {
		t.Skip("set YOLO_LIVE_CHECKOUT_PR to run the live recovery check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	checkout, err := PreparePRCheckoutWithConfig(ctx, prID, PRCheckoutConfig{})
	if err != nil {
		t.Fatalf("PreparePRCheckoutWithConfig(%q) error = %v", prID, err)
	}
	t.Logf("mount path: %s", checkout.MountPath)

	// The checkout must be a functioning arc working copy on the PR branch.
	cmd := exec.CommandContext(ctx, "arc", "rev-parse", "HEAD")
	cmd.Dir = checkout.MountPath
	head, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("arc rev-parse HEAD in checkout failed: %v\n%s", err, head)
	} else {
		t.Logf("checkout HEAD: %s", head)
	}
	if entries, err := os.ReadDir(checkout.MountPath); err != nil || len(entries) == 0 {
		t.Errorf("checkout mount is empty or unreadable: entries=%d err=%v", len(entries), err)
	}

	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(checkout.MountPath)); !os.IsNotExist(err) {
		t.Errorf("mount path still present after cleanup: %v", err)
	}
}
