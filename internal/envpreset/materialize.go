package envpreset

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/agent"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
)

const materializePresetName = "materialize"

func Materialize(ctx context.Context, preset Preset, itemID string) (Workspace, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return Workspace{}, fmt.Errorf("item id is required")
	}

	workspace, err := validateWorkspace(materializePresetName, preset.Workspace)
	if err != nil {
		return Workspace{}, err
	}

	switch workspace.Strategy {
	case WorkspaceStrategyGitClone:
		return materializeGitClone(ctx, workspace, itemID)
	case WorkspaceStrategyArcShared:
		return materializeArcShared(ctx, workspace, itemID)
	case WorkspaceStrategyPath:
		return materializePath(workspace)
	default:
		return Workspace{}, fmt.Errorf("unsupported workspace strategy %q", workspace.Strategy)
	}
}

func materializeGitClone(ctx context.Context, workspace Workspace, itemID string) (Workspace, error) {
	origin, err := expandHome(workspace.Origin)
	if err != nil {
		return Workspace{}, err
	}

	manager := agent.NewGitCloneManager("")
	clonePath, err := manager.CloneForTask(ctx, itemID, origin)
	if err != nil {
		return Workspace{}, err
	}

	cleanup := onceCleanup(func() error {
		return manager.Cleanup(itemID)
	})

	if workspace.BaseBranch != "" {
		runner := materializeCommandRunner{dir: clonePath}
		if output, err := runner.Run("git", "checkout", workspace.BaseBranch); err != nil {
			_ = cleanup()
			return Workspace{}, fmt.Errorf("checkout base branch %q: %s: %w", workspace.BaseBranch, strings.TrimSpace(output), err)
		}
	}

	workspace.Origin = origin
	workspace.Path = clonePath
	workspace.VCS = gitvcs.NewVCSAdapter(materializeCommandRunner{dir: clonePath})
	workspace.Cleanup = cleanup
	return workspace, nil
}

func materializeArcShared(ctx context.Context, workspace Workspace, itemID string) (Workspace, error) {
	mount, err := expandHome(workspace.Mount)
	if err != nil {
		return Workspace{}, err
	}
	lock, err := acquireMaterializeLock(materializeLockKey(mount, workspace.Subpath))
	if err != nil {
		return Workspace{}, err
	}
	cleanup := onceCleanup(lock.Release)

	if err := prepareMaterializeArcMount(ctx, mount); err != nil {
		_ = cleanup()
		return Workspace{}, err
	}
	workspacePath := filepath.Join(mount, workspace.Subpath)
	if stat, err := os.Stat(workspacePath); err != nil {
		_ = cleanup()
		return Workspace{}, fmt.Errorf("arc workspace path %s is not available: %w", workspacePath, err)
	} else if !stat.IsDir() {
		_ = cleanup()
		return Workspace{}, fmt.Errorf("arc workspace path %s is not a directory", workspacePath)
	}

	vcs := arcvcs.New(materializeCommandRunner{dir: workspacePath})
	if _, err := vcs.CreateTaskBranch(ctx, itemID); err != nil {
		_ = cleanup()
		return Workspace{}, err
	}

	workspace.Mount = mount
	workspace.Path = workspacePath
	workspace.VCS = vcs
	workspace.Cleanup = cleanup
	return workspace, nil
}

func prepareMaterializeArcMount(ctx context.Context, mountPath string) error {
	runner := materializeCommandRunner{}
	if mounted, err := materializeArcMountIsMounted(ctx, runner, mountPath); err != nil {
		return err
	} else if mounted {
		return nil
	}

	if err := os.MkdirAll(mountPath, 0o755); err != nil {
		return fmt.Errorf("create arc mount path %s: %w", mountPath, err)
	}

	if out, err := runner.Run("arc", "mount", "-m", mountPath); err != nil {
		details := strings.TrimSpace(out)
		if details == "" {
			return fmt.Errorf("arc mount %s failed: %w", mountPath, err)
		}
		return fmt.Errorf("arc mount %s failed: %s: %w", mountPath, details, err)
	}
	return nil
}

type materializeArcMountEntry struct {
	Status string `json:"status"`
	Mount  string `json:"mount"`
}

func materializeArcMountIsMounted(_ context.Context, runner materializeCommandRunner, mountPath string) (bool, error) {
	out, err := runner.Run("arc", "mount", "-l", "--json")
	if err != nil {
		return false, fmt.Errorf("list arc mounts: %s: %w", strings.TrimSpace(out), err)
	}
	var entries []materializeArcMountEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return false, fmt.Errorf("parse arc mount list: %w", err)
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Status) == "mounted" && filepath.Clean(strings.TrimSpace(entry.Mount)) == filepath.Clean(mountPath) {
			return true, nil
		}
	}
	return false, nil
}

func materializePath(workspace Workspace) (Workspace, error) {
	path, err := expandHome(workspace.Path)
	if err != nil {
		return Workspace{}, err
	}
	if stat, err := os.Stat(path); err != nil {
		return Workspace{}, fmt.Errorf("workspace path %s is not available: %w", path, err)
	} else if !stat.IsDir() {
		return Workspace{}, fmt.Errorf("workspace path %s is not a directory", path)
	}
	workspace.Path = path
	workspace.VCS = nil
	workspace.Cleanup = func() error { return nil }
	return workspace, nil
}

type materializeCommandRunner struct {
	dir string
}

func (r materializeCommandRunner) Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = r.dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func materializeLockKey(mount string, subpath string) string {
	return filepath.Clean(strings.TrimSpace(mount)) + "\x00" + filepath.Clean(strings.TrimSpace(subpath))
}

func materializeLockPath(key string) string {
	return filepath.Join(os.TempDir(), "yolo-runner-envpreset-locks", safeMaterializeLockName(key)+".lock")
}

func safeMaterializeLockName(key string) string {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(key))
	return fmt.Sprintf("%x", hash.Sum64())
}

func onceCleanup(cleanup func() error) func() error {
	var once sync.Once
	var cleanupErr error
	return func() error {
		once.Do(func() {
			cleanupErr = cleanup()
		})
		return cleanupErr
	}
}
