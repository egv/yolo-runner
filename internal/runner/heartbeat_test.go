package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProgressTicker struct {
	ch     chan time.Time
	closed bool
	mu     sync.Mutex
}

func newFakeProgressTicker() *fakeProgressTicker {
	return &fakeProgressTicker{ch: make(chan time.Time)}
}

func (f *fakeProgressTicker) C() <-chan time.Time {
	return f.ch
}

func (f *fakeProgressTicker) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	close(f.ch)
	f.closed = true
}

func (f *fakeProgressTicker) Tick(now time.Time) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return
	}
	f.ch <- now
}

type blockingOpenCode struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingOpenCode) Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	close(b.started)
	<-b.release
	return nil
}

type signalBuffer struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan struct{}
}

func newSignalBuffer() *signalBuffer {
	return &signalBuffer{writes: make(chan struct{}, 1)}
}

func (b *signalBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	n, err := b.buf.Write(p)
	b.mu.Unlock()
	select {
	case b.writes <- struct{}{}:
	default:
	}
	return n, err
}

func (b *signalBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *signalBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForOutputAfter(t *testing.T, buf *signalBuffer, prevLen int) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if buf.Len() > prevLen {
			return buf.String()
		}
		if time.Now().After(deadline) {
			break
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		select {
		case <-buf.writes:
		case <-time.After(remaining):
		}
	}
	t.Fatalf("expected output")
	return ""
}

func lastRender(output string) string {
	parts := strings.Split(output, "\r")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.TrimSpace(parts[i]) != "" {
			return parts[i]
		}
	}
	return ""
}

func TestRunOncePrintsOpenCodeHeartbeat(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}
	logPath := filepath.Join(repoRoot, "runner-logs", "opencode", "task-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("start"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	baseTime := time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(logPath, baseTime, baseTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	ticker := newFakeProgressTicker()
	current := baseTime
	now := func() time.Time { return current }
	output := newSignalBuffer()

	openCode := &blockingOpenCode{started: make(chan struct{}), release: make(chan struct{})}
	deps := RunOnceDeps{
		Beads: &fakeBeads{
			readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
			showQueue:  []Bead{{ID: "task-1", Title: "Task", Status: "open"}},
		},
		Prompt:   &fakePrompt{prompt: "PROMPT"},
		OpenCode: openCode,
		Git:      &fakeGit{dirty: false, rev: "abc123"},
		Logger:   &fakeLogger{},
	}

	resultCh := make(chan struct{})
	var runErr error
	var runResult string
	go func() {
		runResult, runErr = RunOnce(RunOnceOptions{
			RepoRoot:       repoRoot,
			RootID:         "root",
			LogPath:        logPath,
			Out:            output,
			ProgressNow:    now,
			ProgressTicker: ticker,
		}, deps)
		close(resultCh)
	}()

	<-openCode.started
	current = baseTime.Add(6 * time.Second)
	prevLen := output.Len()
	ticker.Tick(current)
	firstOutput := waitForOutputAfter(t, output, prevLen)
	if !strings.Contains(firstOutput, "\r") {
		t.Fatalf("expected carriage return output, got %q", firstOutput)
	}
	if !strings.Contains(firstOutput, "opencode running") {
		t.Fatalf("expected state in output, got %q", firstOutput)
	}
	if !strings.Contains(firstOutput, "last output 6s") {
		t.Fatalf("expected last output age in output, got %q", firstOutput)
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := file.Write([]byte("more")); err != nil {
		_ = file.Close()
		t.Fatalf("append log: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
	current = baseTime.Add(7 * time.Second)
	if err := os.Chtimes(logPath, current, current); err != nil {
		t.Fatalf("chtimes update: %v", err)
	}

	prevLen = output.Len()
	ticker.Tick(current)
	secondOutput := waitForOutputAfter(t, output, prevLen)
	firstLine := lastRender(firstOutput)
	secondLine := lastRender(secondOutput)
	if firstLine == secondLine {
		t.Fatalf("expected spinner to advance")
	}
	if len(firstLine) == 0 || len(secondLine) == 0 || firstLine[0] == secondLine[0] {
		t.Fatalf("expected spinner char to change")
	}

	close(openCode.release)
	select {
	case <-resultCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected RunOnce to finish")
	}
	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if runResult != "blocked" {
		t.Fatalf("expected blocked result, got %q", runResult)
	}

	finished := output.String()
	if !strings.Contains(finished, "OpenCode finished") {
		t.Fatalf("expected finished line, got %q", finished)
	}
	stateIndex := strings.Index(finished, "State: opencode running\n")
	finishIndex := strings.Index(finished, "OpenCode finished")
	if stateIndex == -1 || finishIndex == -1 {
		t.Fatalf("missing state or finish markers")
	}
	segment := finished[stateIndex+len("State: opencode running\n") : finishIndex]
	segment = strings.TrimSuffix(segment, "\n")
	if strings.Contains(segment, "\n") {
		t.Fatalf("expected heartbeat updates without newlines, got %q", segment)
	}
}
