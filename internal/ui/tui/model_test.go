package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

func TestModelRendersTaskAndPhase(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "task-1 - Example Task") {
		t.Fatalf("expected task id and title in view, got %q", view)
	}
	if !strings.Contains(view, "running") {
		t.Fatalf("expected phase in view, got %q", view)
	}
	if !strings.Contains(view, "(5s)") {
		t.Fatalf("expected last output age in view, got %q", view)
	}
}

func TestSpinnerAdvancesOnOutput(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	updated, _ := m.Update(OutputMsg{})
	m = updated.(Model)
	first := m.View()
	updated, _ = m.Update(OutputMsg{})
	m = updated.(Model)
	second := m.View()

	if first == second {
		t.Fatalf("expected spinner to advance, got %q", second)
	}
}

func TestModelTicksLastOutputAge(t *testing.T) {
	now := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	current := now
	m := NewModel(func() time.Time { return current })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: current,
	})
	m = updated.(Model)

	current = current.Add(3 * time.Second)
	updated, cmd := m.Update(tickMsg{})
	m = updated.(Model)

	if cmd == nil {
		t.Fatalf("expected tick command")
	}
	if !strings.Contains(m.View(), "(3s)") {
		t.Fatalf("expected last output age to tick, got %q", m.View())
	}
}

func TestModelOutputResetsLastOutputAge(t *testing.T) {
	now := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	current := now
	m := NewModel(func() time.Time { return current })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: current,
	})
	m = updated.(Model)

	current = current.Add(5 * time.Second)
	updated, _ = m.Update(OutputMsg{})
	m = updated.(Model)

	if !strings.Contains(m.View(), "(0s)") {
		t.Fatalf("expected last output age to reset, got %q", m.View())
	}
}

func TestModelInitSchedulesTick(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("expected tick command")
	}
}

func TestModelShowsQuitHintOnStart(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	view := m.View()
	if !strings.Contains(view, "\nq: stop runner\n") {
		t.Fatalf("expected quit hint in view, got %q", view)
	}
	if strings.Contains(view, "Stopping...") {
		t.Fatalf("did not expect stopping status in view, got %q", view)
	}
}

func TestModelShowsQuitHintWhileStopping(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Stopping...") {
		t.Fatalf("expected stopping status in view, got %q", view)
	}
	if !strings.Contains(view, "\nq: stop runner\n") {
		t.Fatalf("expected quit hint in view, got %q", view)
	}
}

func TestModelStopsOnCtrlC(t *testing.T) {
	stopCh := make(chan struct{})
	m := NewModelWithStop(func() time.Time { return time.Unix(0, 0) }, stopCh)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if !m.StopRequested() {
		t.Fatalf("expected stop requested")
	}
	select {
	case <-stopCh:
		// ok
	default:
		t.Fatalf("expected stop channel to close")
	}
}


func TestModelRendersStatusBarInSingleLine(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	lines := strings.Split(strings.TrimSpace(view), "\n")
	
	// Should have a status bar line containing spinner, phase, and last output age
	statusBarFound := false
	for _, line := range lines {
		if strings.Contains(line, "task-1 - Example Task") {
			// This is the task title line, not status bar
			continue
		}
		if strings.Contains(line, "phase:") {
			// This should not exist anymore - phase should be in status bar
			t.Fatalf("phase should be in status bar, not separate line: %q", line)
		}
		if strings.Contains(line, "last output") {
			// This should not exist anymore - last output should be in status bar
			t.Fatalf("last output should be in status bar, not separate line: %q", line)
		}
		// Check if this line contains spinner, state info, and age
		if strings.Contains(line, "-") || strings.Contains(line, "\\") || strings.Contains(line, "|") || strings.Contains(line, "/") {
			// Found spinner, check if it also contains phase and age info
			if strings.Contains(line, "running") && strings.Contains(line, "5s") {
				statusBarFound = true
			}
		}
	}
	
	if !statusBarFound {
		t.Fatalf("expected status bar line with spinner, phase, and age, got view: %q", view)
	}
}

func TestModelStatusBarUpdatesInPlace(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	current := fixedNow
	m := NewModel(func() time.Time { return current })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: current,
	})
	m = updated.(Model)

	firstView := m.View()
	
	// Advance time and update
	current = current.Add(3 * time.Second)
	updated, _ = m.Update(tickMsg{})
	m = updated.(Model)
	
	secondView := m.View()
	
	// Views should be different (age updated) but structure should be same
	if firstView == secondView {
		t.Fatalf("expected view to change with time")
	}
	
	// Both should have same number of lines
	firstLines := strings.Split(strings.TrimSpace(firstView), "\n")
	secondLines := strings.Split(strings.TrimSpace(secondView), "\n")
	if len(firstLines) != len(secondLines) {
		t.Fatalf("expected same number of lines in status bar updates, got %d vs %d", len(firstLines), len(secondLines))
	}
}

func TestModelStatusBarExactFormat(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	expected := "- task-1 - Example Task\n- running task-1 (5s)\nq: stop runner\n"
	
	if view != expected {
		t.Fatalf("expected exact format %q, got %q", expected, view)
	}
	
	// Test with progress
	updated, _ = m.Update(runner.Event{
		Type:              runner.EventSelectTask,
		IssueID:           "task-1",
		Title:             "Example Task",
		Phase:             "running",
		ProgressCompleted: 2,
		ProgressTotal:     5,
		EmittedAt:         fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)
	
	view = m.View()
	expected = "- [2/5] task-1 - Example Task\n- running task-1 (5s)\nq: stop runner\n"
	
	if view != expected {
		t.Fatalf("expected exact format with progress %q, got %q", expected, view)
	}
}
