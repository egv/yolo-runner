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
	objectStore := strings.TrimSpace(cfg.ObjectsBaseDir)
	if objectStore == "" {
		objectStore = filepath.Join(root, "pr-objects")
	}
	mountsBaseDir := strings.TrimSpace(cfg.MountsBaseDir)
	if mountsBaseDir == "" {
		mountsBaseDir = filepath.Join(root, "pr-mounts")
	}
	mountPath := filepath.Join(mountsBaseDir, prID)
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
			if err := os.RemoveAll(mountPath); err != nil {
				return fmt.Errorf("remove arc PR mount %s: %w", mountPath, err)
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
