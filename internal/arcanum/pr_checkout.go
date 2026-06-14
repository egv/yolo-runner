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

func PreparePRCheckout(prID string) (*PRCheckout, error) {
	return preparePRCheckout(context.Background(), prID)
}

func preparePRCheckout(ctx context.Context, prID string) (*PRCheckout, error) {
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
	objectStore := filepath.Join(root, "pr-objects")
	mountPath := filepath.Join(root, "pr-mounts", prID)
	if err := os.MkdirAll(objectStore, 0o755); err != nil {
		return nil, fmt.Errorf("create arc object store %s: %w", objectStore, err)
	}
	if err := os.MkdirAll(mountPath, 0o755); err != nil {
		return nil, fmt.Errorf("create arc PR mount %s: %w", mountPath, err)
	}

	initArgs := []string{"init", "--repository", "arcadia", "--object-store", objectStore, mountPath}
	if _, stderr, err := arcExec(ctx, "", "arc", initArgs...); err != nil {
		return nil, workspaceArcError("", initArgs, stderr, err)
	}

	checkoutArgs := []string{"pr", "checkout", prID, "--detached", "--force"}
	if _, stderr, err := arcExec(ctx, mountPath, "arc", checkoutArgs...); err != nil {
		return nil, workspaceArcError(mountPath, checkoutArgs, stderr, err)
	}

	return &PRCheckout{
		MountPath: mountPath,
		Cleanup: oncePRCheckoutCleanup(func() error {
			unmountArgs := []string{"unmount", "--forget"}
			if _, stderr, err := arcExec(context.Background(), mountPath, "arc", unmountArgs...); err != nil {
				return workspaceArcError(mountPath, unmountArgs, stderr, err)
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
