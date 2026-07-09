package arcanum

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type PRCheckout struct {
	MountPath string
	Cleanup   func() error
}

type PRCheckoutConfig struct {
	ObjectsBaseDir string
	MountsBaseDir  string
}

func PreparePRCheckout(prID string) (*PRCheckout, error) {
	return PreparePRCheckoutWithConfig(context.Background(), prID, PRCheckoutConfig{})
}

func PreparePRCheckoutWithConfig(ctx context.Context, prID string, cfg PRCheckoutConfig) (*PRCheckout, error) {
	return preparePRCheckout(ctx, prID, cfg)
}

func preparePRCheckout(ctx context.Context, prID string, cfg PRCheckoutConfig) (*PRCheckout, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return nil, fmt.Errorf("PR ID is required")
	}
	if prID == "." || prID == ".." || strings.ContainsAny(prID, `/\`) {
		return nil, fmt.Errorf("PR ID %q cannot be used as a mount path name", prID)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	root := filepath.Join(home, ".yolo-runner")
	objectsBaseDir := strings.TrimSpace(cfg.ObjectsBaseDir)
	if objectsBaseDir == "" {
		objectsBaseDir = filepath.Join(root, "pr-objects")
	}
	mountsBaseDir := strings.TrimSpace(cfg.MountsBaseDir)
	if mountsBaseDir == "" {
		mountsBaseDir = filepath.Join(root, "pr-mounts")
	}
	// Each PR gets its own object store and mount point. A single shared store
	// cannot serve multiple PRs: arc binds a store to the first repository it
	// mounts and rejects subsequent PRs from other branches ("store was
	// previously mounted into different repository"), and a store can only be
	// mounted at one location at a time. Per-PR stores make checkouts fully
	// isolated and parallel-safe.
	objectStore := filepath.Join(objectsBaseDir, prID)
	mountPath := filepath.Join(mountsBaseDir, prID)
	if err := os.MkdirAll(objectStore, 0o755); err != nil {
		return nil, fmt.Errorf("create arc object store %s: %w", objectStore, err)
	}
	// arc mounts onto an existing empty dir; start clean.
	_ = os.RemoveAll(mountPath)
	if err := os.MkdirAll(mountPath, 0o755); err != nil {
		return nil, fmt.Errorf("create arc PR mount %s: %w", mountPath, err)
	}

	// A fresh isolated arc working copy per PR: `arc mount -m <mount> -S <store>`
	// (verified against the arc CLI; `arc init` is for bare/path-filtered repos
	// and rejects this form).
	mountArgs := []string{"mount", "-m", mountPath, "-S", objectStore}
	if _, stderr, err := arcExec(ctx, "", "arc", mountArgs...); err != nil {
		return nil, workspaceArcError("", mountArgs, stderr, err)
	}

	checkoutArgs := []string{"pr", "checkout", prID, "--detached", "--force"}
	if _, stderr, err := arcExec(ctx, mountPath, "arc", checkoutArgs...); err != nil {
		// Best-effort unmount so a failed checkout does not leak a mount.
		_, _, _ = arcExec(context.Background(), "", "arc", "unmount", "--forget", mountPath)
		return nil, workspaceArcError(mountPath, checkoutArgs, stderr, err)
	}

	return &PRCheckout{
		MountPath: mountPath,
		Cleanup: oncePRCheckoutCleanup(func() error {
			unmountArgs := []string{"unmount", "--forget", mountPath}
			if _, stderr, err := arcExec(context.Background(), "", "arc", unmountArgs...); err != nil {
				return workspaceArcError(mountPath, unmountArgs, stderr, err)
			}
			if err := os.RemoveAll(mountPath); err != nil {
				return fmt.Errorf("remove arc PR mount %s: %w", mountPath, err)
			}
			if err := os.RemoveAll(objectStore); err != nil {
				return fmt.Errorf("remove arc PR object store %s: %w", objectStore, err)
			}
			return nil
		}),
	}, nil
}

func oncePRCheckoutCleanup(cleanup func() error) func() error {
	var once sync.Once
	var cleanupErr error
	return func() error {
		once.Do(func() {
			cleanupErr = cleanup()
		})
		return cleanupErr
	}
}
