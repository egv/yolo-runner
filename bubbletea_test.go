package yolo_runner

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type testModel struct{}

func (testModel) Init() tea.Cmd {
	return nil
}

func (testModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return testModel{}, nil
}

func (testModel) View() string {
	return ""
}

func TestBubbleTeaProgramIsBuildable(t *testing.T) {
	program := tea.NewProgram(testModel{})
	if program == nil {
		t.Fatal("expected bubble tea program to be buildable")
	}
}
