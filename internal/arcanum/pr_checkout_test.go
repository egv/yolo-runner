package arcanum

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
			args:      []string{"pr", "checkout", "2293787", "--force"},
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

func TestPreparePRCheckoutReusesAlreadyMountedWorkspace(t *testing.T) {
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
		if len(args) > 0 && args[0] == "mount" {
			return nil, []byte("mount path is already mounted"), errors.New("arc mount failed")
		}
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckout("2293787")
	if err != nil {
		t.Fatalf("PreparePRCheckout() error = %v", err)
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	mountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787")
	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "2293787")
	want := []arcCall{
		{workspace: "", args: []string{"mount", "-m", mountPath, "-S", objectStore}},
		{workspace: mountPath, args: []string{"pr", "checkout", "2293787", "--force"}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("arc calls = %#v, want %#v", calls, want)
	}
}

func TestPreparePRCheckoutRecoversFromStaleMountState(t *testing.T) {
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
	mountAttempts := 0
	arcExec = func(_ context.Context, workspace string, _ string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, arcCall{workspace: workspace, args: append([]string{}, args...)})
		if len(args) > 0 && args[0] == "mount" {
			mountAttempts++
			if mountAttempts == 1 {
				return nil, []byte("Caught (Error 6: Device not configured) util/folder/path.cpp:285: failed to opendir"), errors.New("arc mount failed")
			}
		}
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckout("2293787")
	if err != nil {
		t.Fatalf("PreparePRCheckout() error = %v", err)
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	mountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787")
	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "2293787")
	want := []arcCall{
		{workspace: "", args: []string{"mount", "-m", mountPath, "-S", objectStore}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
		{workspace: "", args: []string{"mount", "-m", mountPath, "-S", objectStore}},
		{workspace: mountPath, args: []string{"pr", "checkout", "2293787", "--force"}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("arc calls = %#v, want %#v", calls, want)
	}
}

func TestPreparePRCheckoutRecoversWhenReusedMountIsDead(t *testing.T) {
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
	mountAttempts := 0
	checkoutAttempts := 0
	arcExec = func(_ context.Context, workspace string, _ string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, arcCall{workspace: workspace, args: append([]string{}, args...)})
		if len(args) > 0 && args[0] == "mount" {
			mountAttempts++
			if mountAttempts == 1 {
				// A stale registry entry reports the path as mounted even though
				// the FUSE process behind it is gone.
				return nil, []byte("mount path is already mounted"), errors.New("arc mount failed")
			}
		}
		if len(args) > 1 && args[0] == "pr" && args[1] == "checkout" {
			checkoutAttempts++
			if checkoutAttempts == 1 {
				return nil, []byte("Not a mounted arc repository. Did you forget to mount arcadia?"), errors.New("arc pr checkout failed")
			}
		}
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckout("2293787")
	if err != nil {
		t.Fatalf("PreparePRCheckout() error = %v", err)
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	mountPath := filepath.Join(home, ".yolo-runner", "pr-mounts", "2293787")
	objectStore := filepath.Join(home, ".yolo-runner", "pr-objects", "2293787")
	want := []arcCall{
		{workspace: "", args: []string{"mount", "-m", mountPath, "-S", objectStore}},
		{workspace: mountPath, args: []string{"pr", "checkout", "2293787", "--force"}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
		{workspace: "", args: []string{"mount", "-m", mountPath, "-S", objectStore}},
		{workspace: mountPath, args: []string{"pr", "checkout", "2293787", "--force"}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("arc calls = %#v, want %#v", calls, want)
	}
}

func TestPreparePRCheckoutRetriesStaleStateOnlyOnce(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	mountAttempts := 0
	arcExec = func(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, error) {
		if len(args) > 0 && args[0] == "mount" {
			mountAttempts++
			return nil, []byte("Caught (Error 6: Device not configured)"), errors.New("arc mount failed")
		}
		return nil, nil, nil
	}

	if _, err := PreparePRCheckout("2293787"); err == nil {
		t.Fatal("PreparePRCheckout() error = nil, want persistent stale-state failure")
	}
	if mountAttempts != 2 {
		t.Fatalf("mount attempts = %d, want 2", mountAttempts)
	}
}

func TestPreparePRCheckoutDoesNotRetryNonStaleFailures(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	checkoutAttempts := 0
	arcExec = func(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, error) {
		if len(args) > 1 && args[0] == "pr" && args[1] == "checkout" {
			checkoutAttempts++
			return nil, []byte("pull request 2293787 not found"), errors.New("arc pr checkout failed")
		}
		return nil, nil, nil
	}

	if _, err := PreparePRCheckout("2293787"); err == nil {
		t.Fatal("PreparePRCheckout() error = nil, want checkout failure")
	}
	if checkoutAttempts != 1 {
		t.Fatalf("checkout attempts = %d, want 1", checkoutAttempts)
	}
}

func TestArcStaleCheckoutErrorClassifiesRebaseConflictAsGenuine(t *testing.T) {
	conflict := errors.New("arc rebase trunk in workspace /mounts/1 failed: there are some conflicts:\n    content  services/x/ya.make\nrebase wasn't performed.")
	if arcStaleCheckoutError(conflict) {
		t.Fatal("arcStaleCheckoutError() = true for a rebase conflict, want false")
	}
	stale := errors.New("arc push -f in workspace /mounts/1 failed: Not a mounted arc repository. Did you forget to mount arcadia?")
	if !arcStaleCheckoutError(stale) {
		t.Fatal("arcStaleCheckoutError() = false for a dead mount error, want true")
	}
}

func TestPreparePRCheckoutRebasesAndPushesAuthorPRBeforeUse(t *testing.T) {
	oldExec := arcExec
	oldPublishAndVerify := publishAndVerifyPRCheckout
	t.Cleanup(func() {
		arcExec = oldExec
		publishAndVerifyPRCheckout = oldPublishAndVerify
	})
	publishAndVerifyPRCheckout = func(ctx context.Context, prID string, publish PRPublishFunc, _ PRPublicationVerifier) error {
		return publish(ctx, prID)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	type arcCall struct {
		workspace string
		args      []string
	}
	var calls []arcCall
	revParseCalls := 0
	arcExec = func(_ context.Context, workspace string, _ string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, arcCall{workspace: workspace, args: append([]string{}, args...)})
		if reflect.DeepEqual(args, []string{"pr", "status", "--json", "2293787"}) {
			return []byte(`{"id":2293787,"status":"open","from_id":"head","to_branch":"trunk"}`), nil, nil
		}
		if reflect.DeepEqual(args, []string{"rev-parse", "HEAD"}) {
			// The rebase moves HEAD: pre-rebase and post-rebase revisions differ.
			revParseCalls++
			if revParseCalls == 1 {
				return []byte("old-head\n"), nil, nil
			}
			return []byte("head\n"), nil, nil
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
		{workspace: mountPath, args: []string{"pr", "checkout", "2293787", "--force"}},
		{workspace: mountPath, args: []string{"pr", "status", "--json", "2293787"}},
		{workspace: mountPath, args: []string{"rev-parse", "HEAD"}},
		{workspace: mountPath, args: []string{"rebase", "trunk"}},
		{workspace: mountPath, args: []string{"rev-parse", "HEAD"}},
		{workspace: mountPath, args: []string{"push", "-f"}},
		{workspace: mountPath, args: []string{"pr", "publish", "2293787"}},
		{workspace: "", args: []string{"unmount", "--force", "--forget", mountPath}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("arc calls = %#v, want %#v", calls, want)
	}
}

// A rebase that does not move HEAD means the remote PR already has exactly
// this revision published. Pushing and republishing anyway would mint a new
// Arcanum iteration with zero content changes and re-trigger every automated
// reviewer watching the PR.
func TestPreparePRCheckoutSkipsPushAndPublishWhenRebaseIsNoOp(t *testing.T) {
	oldExec := arcExec
	oldPublishAndVerify := publishAndVerifyPRCheckout
	t.Cleanup(func() {
		arcExec = oldExec
		publishAndVerifyPRCheckout = oldPublishAndVerify
	})
	publishAndVerifyPRCheckout = func(ctx context.Context, prID string, publish PRPublishFunc, _ PRPublicationVerifier) error {
		t.Fatal("publish must not run for a no-op rebase")
		return nil
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	var pushed bool
	arcExec = func(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, error) {
		if reflect.DeepEqual(args, []string{"pr", "status", "--json", "2293787"}) {
			return []byte(`{"id":2293787,"status":"open","from_id":"head","to_branch":"trunk"}`), nil, nil
		}
		if reflect.DeepEqual(args, []string{"rev-parse", "HEAD"}) {
			return []byte("same-head\n"), nil, nil
		}
		if len(args) > 0 && args[0] == "push" {
			pushed = true
		}
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckoutWithConfig(context.Background(), "2293787", PRCheckoutConfig{Rebase: true})
	if err != nil {
		t.Fatalf("PreparePRCheckoutWithConfig() error = %v", err)
	}
	if pushed {
		t.Fatal("arc push ran for a no-op rebase")
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

// A rebase that stops on merge conflicts is the coding agent's job, not a
// terminal failure: the checkout is returned usable (rebase aborted, clean
// tree on the PR head) with the conflict attached, and nothing is pushed or
// published.
func TestPreparePRCheckoutSurfacesRebaseConflictInsteadOfFailing(t *testing.T) {
	oldExec := arcExec
	oldPublishAndVerify := publishAndVerifyPRCheckout
	t.Cleanup(func() {
		arcExec = oldExec
		publishAndVerifyPRCheckout = oldPublishAndVerify
	})
	publishAndVerifyPRCheckout = func(ctx context.Context, prID string, publish PRPublishFunc, _ PRPublicationVerifier) error {
		t.Fatal("publish must not run when the rebase conflicted")
		return nil
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	aborted := false
	pushed := false
	arcExec = func(_ context.Context, _ string, _ string, args ...string) ([]byte, []byte, error) {
		if reflect.DeepEqual(args, []string{"pr", "status", "--json", "2293787"}) {
			return []byte(`{"id":2293787,"status":"open","from_id":"head","to_branch":"trunk"}`), nil, nil
		}
		if reflect.DeepEqual(args, []string{"rev-parse", "HEAD"}) {
			return []byte("head\n"), nil, nil
		}
		if reflect.DeepEqual(args, []string{"rebase", "trunk"}) {
			return nil, []byte("there are some conflicts:\n    content  services/x/ya.make  068544fc\nrebase wasn't performed.\n"), errors.New("exit status 1")
		}
		if reflect.DeepEqual(args, []string{"rebase", "--abort"}) {
			aborted = true
		}
		if len(args) > 0 && args[0] == "push" {
			pushed = true
		}
		return nil, nil, nil
	}

	checkout, err := PreparePRCheckoutWithConfig(context.Background(), "2293787", PRCheckoutConfig{Rebase: true})
	if err != nil {
		t.Fatalf("PreparePRCheckoutWithConfig() error = %v, want conflict surfaced on the checkout", err)
	}
	if checkout.RebaseConflict == nil {
		t.Fatal("checkout.RebaseConflict = nil, want conflict details")
	}
	if checkout.RebaseConflict.TargetBranch != "trunk" {
		t.Fatalf("conflict target = %q, want trunk", checkout.RebaseConflict.TargetBranch)
	}
	if !strings.Contains(checkout.RebaseConflict.Details, "services/x/ya.make") {
		t.Fatalf("conflict details = %q, want the conflicted path", checkout.RebaseConflict.Details)
	}
	if !aborted {
		t.Fatal("arc rebase --abort was not run after the conflict")
	}
	if pushed {
		t.Fatal("arc push ran despite the conflicted rebase")
	}
	if err := checkout.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
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
