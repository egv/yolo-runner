package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestBoardModelNumberKeysSwitchTabs(t *testing.T) {
	board := newBoardModel(boardConfig{}, nil, nil)

	for _, tc := range []struct {
		key  string
		want boardTab
	}{
		{key: "2", want: boardTabQueue},
		{key: "3", want: boardTabRunners},
		{key: "1", want: boardTabCollectors},
	} {
		updated, _ := board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)})
		board = updated.(boardModel)
		if board.activeTab != tc.want {
			t.Fatalf("after key %q activeTab = %d, want %d", tc.key, board.activeTab, tc.want)
		}
	}
}

func TestBoardModelListNavigationMovesActiveTabCursor(t *testing.T) {
	board := newBoardModel(boardConfig{}, nil, nil)
	board.snapshot = boardSnapshot{
		items: []workitem.Item{
			{ID: "item-1"},
			{ID: "item-2"},
		},
		runners: []workqueue.RunnerRow{
			{ID: "runner-1"},
			{ID: "runner-2"},
		},
		sources: []workqueue.SourceRow{
			{Source: "github", State: "pending", Count: 1},
			{Source: "startrek", State: "pending", Count: 1},
		},
	}

	updated, _ := board.Update(tea.KeyMsg{Type: tea.KeyDown})
	board = updated.(boardModel)
	if board.collectorCur != 1 {
		t.Fatalf("collectorCur = %d, want 1", board.collectorCur)
	}
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	board = updated.(boardModel)
	if board.collectorCur != 0 {
		t.Fatalf("collectorCur = %d, want 0", board.collectorCur)
	}

	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	board = updated.(boardModel)
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	board = updated.(boardModel)
	if board.queueCur != 1 {
		t.Fatalf("queueCur = %d, want 1", board.queueCur)
	}
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyUp})
	board = updated.(boardModel)
	if board.queueCur != 0 {
		t.Fatalf("queueCur = %d, want 0", board.queueCur)
	}

	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	board = updated.(boardModel)
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyDown})
	board = updated.(boardModel)
	if board.runnerCur != 1 {
		t.Fatalf("runnerCur = %d, want 1", board.runnerCur)
	}
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	board = updated.(boardModel)
	if board.runnerCur != 0 {
		t.Fatalf("runnerCur = %d, want 0", board.runnerCur)
	}
}

func TestBoardModelQuitKeysReturnQuitCommand(t *testing.T) {
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	} {
		board := newBoardModel(boardConfig{}, nil, nil)
		_, cmd := board.Update(msg)
		if cmd == nil {
			t.Fatalf("Update(%q) command = nil, want quit command", msg.String())
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("Update(%q) command message type = %T, want tea.QuitMsg", msg.String(), cmd())
		}
	}
}
