package arcanum

import (
	"context"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
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

	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "2293787")
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
			args:      []string{"unmount", "--force", "--forget", mountPath},
		},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("arc calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestPreparePRCheckoutRebasesAndPushesAuthorPRBeforeUse(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	type arcCall struct {
		workspace string
		args      []string
	}
	var calls []arcCall
	arcExec = func(_ context.Context, workspace string, _ string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, arcCall{workspace: workspace, args: append([]string{}, args...)})
		if reflect.DeepEqual(args, []string{"pr", "status", "--json", "2293787"}) {
			return []byte(`{"id":2293787,"status":"open","from_id":"head","to_branch":"trunk"}`), nil, nil
		}
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckoutWithConfig(context.Background(), "2293787", PRCheckoutConfig{Rebase: true})
	if err != nil {
		t.Fatalf("PreparePRCheckoutWithConfig() error = %v", err)
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	mountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787")
	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "2293787")
	want := []arcCall{
		{workspace: "", args: []string{"mount", "-m", mountPath, "-S", objectStore}},
		{workspace: mountPath, args: []string{"pr", "checkout", "2293787", "--detached", "--force"}},
		{workspace: mountPath, args: []string{"pr", "status", "--json", "2293787"}},
		{workspace: mountPath, args: []string{"rebase", "trunk"}},
		{workspace: mountPath, args: []string{"push", "-f"}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("arc calls = %#v, want %#v", calls, want)
	}
}

func TestPreparePRCheckoutConcurrentCallsUseDistinctMountsAndPerPRObjectStores(t *testing.T) {
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

	// Each PR gets its own distinct mount path AND its own object store. A
	// shared store cannot serve PRs from different branches, so per-PR stores
	// make checkouts fully isolated and parallel-safe.
	mounts := []string{inits[0].mountPath, inits[1].mountPath}
	stores := []string{inits[0].objectStore, inits[1].objectStore}
	sort.Strings(mounts)
	sort.Strings(stores)
	wantMounts := []string{
		filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787"),
		filepath.Join(home, ".yolo-runner", "pr-mounts", "2293788"),
	}
	wantStores := []string{
		filepath.Join(home, ".yolo-runner", "pr-objects", "2293787"),
		filepath.Join(home, ".yolo-runner", "pr-objects", "2293788"),
	}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Fatalf("mount paths = %#v, want %#v", mounts, wantMounts)
	}
	if !reflect.DeepEqual(stores, wantStores) {
		t.Fatalf("object stores = %#v, want %#v", stores, wantStores)
	}
}

func TestPreparePRCheckoutSerializesSamePRUntilCleanup(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	var (
		mu         sync.Mutex
		mountCalls int
	)
	secondMount := make(chan struct{})
	arcExec = func(_ context.Context, _ string, name string, args ...string) ([]byte, []byte, error) {
		if name == "arc" && len(args) == 5 && args[0] == "mount" {
			mu.Lock()
			mountCalls++
			if mountCalls == 2 {
				close(secondMount)
			}
			mu.Unlock()
		}
		return nil, nil, nil
	}

	first, err := PreparePRCheckout("2293787")
	if err != nil {
		t.Fatalf("first PreparePRCheckout() error = %v", err)
	}

	type checkoutResult struct {
		checkout *PRCheckout
		err      error
	}
	secondResult := make(chan checkoutResult, 1)
	go func() {
		checkout, prepareErr := PreparePRCheckout("2293787")
		secondResult <- checkoutResult{checkout: checkout, err: prepareErr}
	}()

	select {
	case <-secondMount:
		t.Fatal("second checkout mounted before first cleanup")
	case <-time.After(100 * time.Millisecond):
	}

	if err := first.Cleanup(); err != nil {
		t.Fatalf("first Cleanup() error = %v", err)
	}

	var second checkoutResult
	select {
	case second = <-secondResult:
	case <-time.After(2 * time.Second):
		t.Fatal("second checkout did not proceed after first cleanup")
	}
	if second.err != nil {
		t.Fatalf("second PreparePRCheckout() error = %v", second.err)
	}
	if err := second.checkout.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}
