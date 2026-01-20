package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"yolo-runner/internal/runner"
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
	if !strings.Contains(view, "last runner event 5s") {
		t.Fatalf("expected last runner event age in view, got %q", view)
	}
}

func TestModelRendersProgressIndicator(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:              runner.EventSelectTask,
		IssueID:           "task-1",
		Title:             "Example Task",
		Phase:             "running",
		EmittedAt:         fixedNow,
		ProgressCompleted: 1,
		ProgressTotal:     3,
	})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "[1/3] task-1 - Example Task") {
		t.Fatalf("expected progress indicator in view, got %q", view)
	}

	updated, _ = m.Update(runner.Event{
		Type:              runner.EventSelectTask,
		IssueID:           "task-2",
		Title:             "Next Task",
		Phase:             "running",
		EmittedAt:         fixedNow,
		ProgressCompleted: 2,
		ProgressTotal:     3,
	})
	m = updated.(Model)

	view = m.View()
	if !strings.Contains(view, "[2/3] task-2 - Next Task") {
		t.Fatalf("expected updated progress indicator in view, got %q", view)
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
	if !strings.Contains(m.View(), "last runner event 3s") {
		t.Fatalf("expected last runner event age to tick, got %q", m.View())
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

	if !strings.Contains(m.View(), "last runner event 0s") {
		t.Fatalf("expected last runner event age to reset, got %q", m.View())
	}
}

func TestModelInitSchedulesTick(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("expected tick command")
	}
}

func TestModelStopKeySetsStoppingState(t *testing.T) {
	stopCh := make(chan struct{})
	m := NewModelWithStop(func() time.Time { return time.Unix(0, 0) }, stopCh)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)

	if !m.StopRequested() {
		t.Fatalf("expected stop to be requested")
	}
	if cmd == nil {
		t.Fatalf("expected stop command")
	}
	select {
	case <-stopCh:
		// ok
	default:
		t.Fatalf("expected stop channel to close")
	}
	if !strings.Contains(m.View(), "Stopping...") {
		t.Fatalf("expected stopping view, got %q", m.View())
	}
}

func TestModelStopKeyClosesStopChannel(t *testing.T) {
	stopCh := make(chan struct{})
	m := NewModelWithStop(func() time.Time { return time.Unix(0, 0) }, stopCh)
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	select {
	case <-stopCh:
		// ok
	default:
		t.Fatalf("expected stop channel to close")
	}
}
