package main

import (
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type boardSnapshot struct {
	items           []workitem.Item
	runners         []workqueue.RunnerRow
	currentByRunner map[string]*workitem.Item
	sources         []workqueue.SourceRow
	stateCounts     map[string]int
	runtimeByItem   map[string]itemRuntimeSnapshot
}

type itemRuntimeSnapshot struct {
	phase       string
	output      string
	lastError   string
	lastEventAt time.Time
}

func (s boardSnapshot) itemCount() int {
	return len(s.items)
}

func (s boardSnapshot) sourceCount() int {
	seen := map[string]struct{}{}
	for _, source := range s.sources {
		seen[source.Source] = struct{}{}
	}
	return len(seen)
}

func (s *boardSnapshot) applyPoll(poll boardSnapshot) {
	previousRuntime := s.runtimeByItem
	*s = poll
	if len(previousRuntime) == 0 {
		s.runtimeByItem = runtimeFromPollItems(poll.items)
		return
	}

	s.runtimeByItem = make(map[string]itemRuntimeSnapshot, len(previousRuntime)+len(poll.items))
	for _, item := range poll.items {
		polled := itemRuntimeSnapshot{phase: item.State}
		rowTimestamp := itemRowTimestamp(item)
		if previous, ok := previousRuntime[item.ID]; ok && previous.lastEventAt.After(rowTimestamp) {
			s.runtimeByItem[item.ID] = previous
			continue
		}
		s.runtimeByItem[item.ID] = polled
	}
}

func (s *boardSnapshot) applyEvent(event contracts.Event) {
	itemID := event.ItemID
	if itemID == "" {
		itemID = event.TaskID
	}
	if itemID == "" {
		return
	}
	if s.runtimeByItem == nil {
		s.runtimeByItem = make(map[string]itemRuntimeSnapshot)
	}

	runtime := s.runtimeByItem[itemID]
	runtime.phase = string(event.Type)
	if event.Message != "" {
		runtime.output = event.Message
	}
	if event.Detail != "" {
		runtime.lastError = event.Detail
	} else if event.Type == contracts.EventTypeTaskFailed || event.Type == contracts.EventTypeAgentBlocked {
		runtime.lastError = event.Message
	}
	runtime.lastEventAt = event.Timestamp
	s.runtimeByItem[itemID] = runtime
}

func runtimeFromPollItems(items []workitem.Item) map[string]itemRuntimeSnapshot {
	if len(items) == 0 {
		return nil
	}
	runtimeByItem := make(map[string]itemRuntimeSnapshot, len(items))
	for _, item := range items {
		runtimeByItem[item.ID] = itemRuntimeSnapshot{phase: item.State}
	}
	return runtimeByItem
}

func itemRowTimestamp(item workitem.Item) time.Time {
	if item.HeartbeatAt.After(item.UpdatedAt) {
		return item.HeartbeatAt
	}
	return item.UpdatedAt
}
