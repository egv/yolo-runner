package arcanum

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestPreparePRCheckoutInitializesChecksOutAndCleansUp(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	type arcCall struct {
		workspace string
		name      string
		args      []string
	}
	var calls []arcCall
	arcExec = func(_ context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, arcCall{
			workspace: workspace,
			name:      name,
			args:      append([]string{}, args...),
		})
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckout("2293787")
	if err != nil {
		t.Fatalf("PreparePRCheckout() error = %v", err)
	}

	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects")
	mountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787")
	if checkout.MountPath != mountPath {
		t.Fatalf("PreparePRCheckout() mount path = %q, want %q", checkout.MountPath, mountPath)
	}

	if checkout.Cleanup == nil {
		t.Fatal("PreparePRCheckout() Cleanup = nil")
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	wantCalls := []arcCall{
		{
			workspace: "",
			name:      "arc",
			args:      []string{"mount", "-m", mountPath, "-S", objectStore},
		},
		{
			workspace: mountPath,
			name:      "arc",
			args:      []string{"pr", "checkout", "2293787", "--detached", "--force"},
		},
		{
			workspace: "",
			name:      "arc",
			args:      []string{"unmount", "--forget", mountPath},
		},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("arc calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestPreparePRCheckoutConcurrentCallsUseDistinctMountsAndSharedObjectStore(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	type initCall struct {
		objectStore string
		mountPath   string
	}
	var (
		mu    sync.Mutex
		inits []initCall
	)
	arcExec = func(_ context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		mu.Lock()
		defer mu.Unlock()

		if name == "arc" && len(args) == 5 && args[0] == "mount" {
			// arc mount -m <mountPath> -S <objectStore>
			inits = append(inits, initCall{
				objectStore: args[4],
				mountPath:   args[2],
			})
		}
		return nil, nil, nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, prID := range []string{"2293787", "2293788"} {
		wg.Add(1)
		go func(prID string) {
			defer wg.Done()
			_, err := PreparePRCheckout(prID)
			errs <- err
		}(prID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("PreparePRCheckout() error = %v", err)
		}
	}

	if len(inits) != 2 {
		t.Fatalf("init calls = %d, want 2", len(inits))
	}

	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects")
	mounts := []string{inits[0].mountPath, inits[1].mountPath}
	sort.Strings(mounts)
	wantMounts := []string{
		filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787"),
		filepath.Join(home, ".yolo-runner", "pr-mounts", "2293788"),
	}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Fatalf("mount paths = %#v, want %#v", mounts, wantMounts)
	}
	for _, call := range inits {
		if call.objectStore != objectStore {
			t.Fatalf("object store = %q, want %q", call.objectStore, objectStore)
		}
	}
}
