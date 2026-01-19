package tui

import (
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(view, "last output 5s") {
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
	if !strings.Contains(m.View(), "last output 3s") {
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

	if !strings.Contains(m.View(), "last output 0s") {
		t.Fatalf("expected last output age to reset, got %q", m.View())
	}
}

func TestModelInitSchedulesTick(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("expected tick command")
	}
}
