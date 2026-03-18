package codingagents

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestResolveCommandArgsRendersBackendPlaceholders(t *testing.T) {
	t.Helper()
	got := CommandSpec{}
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		got = spec
		return nil
	})
	adapter := NewGenericCLIRunnerAdapter("custom-cli", "/usr/bin/custom-cli", []string{
		"--backend={{backend}}",
		"--backend-name={{backend-name}}",
		"--model={{model}}",
		"{{prompt}}",
	}, runner)

	_, _ = adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "custom-model",
		Prompt:   "Implement feature",
		TaskID:   "task-1",
		RepoRoot: t.TempDir(),
		Metadata: nil,
		Mode:     contracts.RunnerModeImplement,
	})

	if got.Binary != "/usr/bin/custom-cli" {
		t.Fatalf("expected binary %q, got %q", "/usr/bin/custom-cli", got.Binary)
	}
	if !containsSlice(got.Args, "--backend=custom-cli") {
		t.Fatalf("expected --backend placeholder to render, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "--backend-name=custom-cli") {
		t.Fatalf("expected --backend-name placeholder to render, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "--model=custom-model") {
		t.Fatalf("expected --model placeholder to render, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "Implement feature") {
		t.Fatalf("expected prompt placeholder to render, got %#v", got.Args)
	}
}

func TestResolveCommandArgsPreservesNonTemplateValues(t *testing.T) {
	t.Helper()
	got := CommandSpec{}
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		got = spec
		return nil
	})
	adapter := NewGenericCLIRunnerAdapter("", "custom", []string{
		"echo",
		"hello",
	}, runner)

	_, _ = adapter.Run(context.Background(), contracts.RunnerRequest{
		Prompt:   "anything",
		Mode:     contracts.RunnerModeImplement,
		Model:    "model-x",
		TaskID:   "task-2",
		RepoRoot: t.TempDir(),
	})

	if len(got.Args) != 2 {
		t.Fatalf("expected unchanged args count=2, got %d", len(got.Args))
	}
	if got.Args[0] != "echo" || got.Args[1] != "hello" {
		t.Fatalf("expected non-template args preserved, got %#v", got.Args)
	}
}

func TestGeminiBackendAdapterRendersModelInArgs(t *testing.T) {
	t.Helper()
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	definition, ok := catalog.Backend("gemini")
	if !ok {
		t.Fatalf("expected builtin gemini backend")
	}

	got := CommandSpec{}
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		got = spec
		return nil
	})
	adapter := NewGenericCLIRunnerAdapter(definition.Name, definition.Binary, definition.Args, runner)

	_, _ = adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "gemini-2.0-pro",
		Prompt:   "Implement feature",
		TaskID:   "task-1",
		RepoRoot: t.TempDir(),
	})

	if got.Binary != "gemini" {
		t.Fatalf("expected binary %q, got %q", "gemini", got.Binary)
	}
	if !containsSlice(got.Args, "exec") {
		t.Fatalf("expected exec arg to be rendered, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "--model") {
		t.Fatalf("expected --model flag to be rendered, got %#v", got.Args)
	}
	if !containsSlice(got.Args, "gemini-2.0-pro") {
		t.Fatalf("expected selected model in args, got %#v", got.Args)
	}
}

func TestGenericCLIRunnerAdapterUsesLastStructuredReviewVerdictInReviewMode(t *testing.T) {
	t.Helper()
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, err := io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		if err != nil {
			return err
		}
		_, err = io.WriteString(spec.Stdout, "REVIEW_VERDICT: fail\n")
		return err
	})
	adapter := NewGenericCLIRunnerAdapter("codex", "mock", []string{"exec"}, runner)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeReview,
		TaskID:   "task-1",
		RepoRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := strings.TrimSpace(result.Artifacts["review_verdict"]); got != "fail" {
		t.Fatalf("expected review verdict from last marker, got %q", got)
	}
	if result.ReviewReady {
		t.Fatalf("expected review_ready false for fail verdict")
	}
}

func TestGenericCLIRunnerAdapterLeavesReviewFieldsUnsetOutsideReviewMode(t *testing.T) {
	t.Helper()
	runner := commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, err := io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		return err
	})
	adapter := NewGenericCLIRunnerAdapter("codex", "mock", []string{"exec"}, runner)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeImplement,
		TaskID:   "task-2",
		RepoRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, ok := result.Artifacts["review_verdict"]; ok {
		t.Fatalf("did not expect review_verdict in non-review mode, got %#v", result.Artifacts)
	}
	if result.ReviewReady {
		t.Fatalf("did not expect ReviewReady in non-review mode")
	}
}

type fakeManagedProcess struct {
	waitCh    chan error
	stopCh    chan error
	killCh    chan error
	waitDone  chan struct{}
	waitOnce  sync.Once
	waitErr   error
	stopCount int
	killCount int
}

func newFakeManagedProcess() *fakeManagedProcess {
	return &fakeManagedProcess{
		waitCh:   make(chan error, 1),
		stopCh:   make(chan error, 1),
		killCh:   make(chan error, 1),
		waitDone: make(chan struct{}),
	}
}

func (p *fakeManagedProcess) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = <-p.waitCh
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

func (p *fakeManagedProcess) WaitChan() <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- p.Wait()
	}()
	return done
}

func (p *fakeManagedProcess) Stop() error {
	p.stopCount++
	return <-p.stopCh
}

func (p *fakeManagedProcess) Kill() error {
	p.killCount++
	return <-p.killCh
}

func TestGenericCLIRunnerAdapterManagedRunCleansUpViaProcessSupervisor(t *testing.T) {
	t.Helper()

	proc := newFakeManagedProcess()
	proc.stopCh <- nil
	ready := make(chan struct{})
	go func() {
		<-ready
		proc.waitCh <- nil
	}()

	var startedSpec CommandSpec
	adapter := NewGenericCLIRunnerAdapter("custom-cli", "/usr/bin/custom-cli", []string{"serve"}, nil).WithStarter(commandStarterFunc(func(_ context.Context, spec CommandSpec) (SupervisedProcess, error) {
		startedSpec = spec
		return proc, nil
	}))
	adapter.waitReady = func(context.Context, SupervisedProcess) error {
		close(ready)
		return nil
	}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-3",
		RepoRoot: t.TempDir(),
		Prompt:   "serve task",
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %q (%s)", result.Status, result.Reason)
	}
	if startedSpec.Binary != "/usr/bin/custom-cli" {
		t.Fatalf("expected managed start binary %q, got %q", "/usr/bin/custom-cli", startedSpec.Binary)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected process supervisor cleanup stop, got %d", proc.stopCount)
	}
}

func TestGenericCLIRunnerAdapterManagedRunReturnsEarlyExitBeforeReadiness(t *testing.T) {
	t.Helper()

	proc := newFakeManagedProcess()
	proc.stopCh <- nil
	exitErr := errors.New("backend exited before ready")
	proc.waitCh <- exitErr

	adapter := NewGenericCLIRunnerAdapter("custom-cli", "/usr/bin/custom-cli", []string{"serve"}, nil).WithStarter(commandStarterFunc(func(_ context.Context, _ CommandSpec) (SupervisedProcess, error) {
		return proc, nil
	}))
	adapter.waitReady = func(ctx context.Context, _ SupervisedProcess) error {
		<-ctx.Done()
		return ctx.Err()
	}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-4",
		RepoRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status for pre-readiness exit, got %q", result.Status)
	}
	if !strings.Contains(result.Reason, exitErr.Error()) {
		t.Fatalf("expected early exit reason %q, got %q", exitErr.Error(), result.Reason)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected cleanup stop after readiness failure, got %d", proc.stopCount)
	}
}

func TestGenericCLIRunnerAdapterManagedRunKillsStalledReadiness(t *testing.T) {
	t.Helper()

	proc := newFakeManagedProcess()
	proc.killCh <- nil

	adapter := NewGenericCLIRunnerAdapter("custom-cli", "/usr/bin/custom-cli", []string{"serve"}, nil).WithStarter(commandStarterFunc(func(_ context.Context, _ CommandSpec) (SupervisedProcess, error) {
		return proc, nil
	}))
	adapter.waitReady = func(ctx context.Context, _ SupervisedProcess) error {
		<-ctx.Done()
		return ctx.Err()
	}
	adapter.gracePeriod = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		result, err := adapter.Run(ctx, contracts.RunnerRequest{
			TaskID:   "task-5",
			RepoRoot: t.TempDir(),
			Timeout:  5 * time.Millisecond,
		})
		if err != nil {
			done <- err
			return
		}
		if result.Status != contracts.RunnerResultBlocked {
			done <- errors.New("expected blocked status")
			return
		}
		if !strings.Contains(result.Reason, "runner timeout") {
			done <- errors.New("expected runner timeout reason")
			return
		}
		done <- nil
	}()

	time.Sleep(20 * time.Millisecond)
	proc.waitCh <- nil

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected readiness timeout result, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected managed run to return after readiness timeout")
	}

	if proc.stopCount != 1 {
		t.Fatalf("expected graceful stop attempt on readiness timeout, got %d", proc.stopCount)
	}
	if proc.killCount != 1 {
		t.Fatalf("expected forced kill on readiness timeout, got %d", proc.killCount)
	}
}

func TestGenericCLIRunnerAdapterDefaultPathUsesManagedSupervisorForReadinessTimeout(t *testing.T) {
	t.Helper()

	tempDir := t.TempDir()
	stopMarker := filepath.Join(tempDir, "stopped")

	adapter := NewGenericCLIRunnerAdapter("custom-cli", os.Args[0], []string{
		"-test.run=^TestGenericCLIRunnerAdapterManagedSupervisorHelper$",
		"--",
		stopMarker,
	}, nil).WithHealthConfig(&BackendHealthConfig{
		Enabled:  true,
		Command:  "false",
		Timeout:  "5ms",
		Interval: "5ms",
	})
	adapter.gracePeriod = 50 * time.Millisecond

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-6",
		RepoRoot: tempDir,
		Timeout:  30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status from readiness timeout, got %q (%s)", result.Status, result.Reason)
	}
	if !strings.Contains(result.Reason, "runner timeout") {
		t.Fatalf("expected runner timeout reason, got %q", result.Reason)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		content, readErr := os.ReadFile(stopMarker)
		if readErr == nil {
			if strings.TrimSpace(string(content)) != "stopped" {
				t.Fatalf("expected graceful stop marker, got %q", strings.TrimSpace(string(content)))
			}
			break
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			t.Fatalf("read stop marker: %v", readErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected managed supervisor to interrupt process and create %s", stopMarker)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGenericCLIRunnerAdapterManagedSupervisorHelper(t *testing.T) {
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || len(args) <= sep+1 {
		return
	}

	stopMarker := args[sep+1]
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	for {
		select {
		case <-sigCh:
			if err := os.WriteFile(stopMarker, []byte("stopped"), 0o644); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func containsSlice(values []string, needle string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}
