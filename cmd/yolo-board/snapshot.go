package main

import (
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type boardSnapshot struct {
	items             []workitem.Item
	queueItemsMore    int
	runners           []workqueue.RunnerRow
	currentByRunner   map[string]*workitem.Item
	sources           []workqueue.SourceRow
	stateCounts       map[string]int
	runtimeByItem     map[string]itemRuntimeSnapshot
	unconsumedResults []workqueue.UnconsumedResult
	collectorErrors   map[string]collectorErrorBucket
}

type itemRuntimeSnapshot struct {
	phase       string
	output      string
	lastError   string
	lastEventAt time.Time
}

type collectorErrorBucket struct {
	source  string
	message string
	count   int
	lastAt  time.Time
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
	previousCollectorErrors := s.collectorErrors
	*s = poll
	s.collectorErrors = previousCollectorErrors
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
	s.applyCollectorErrorEvent(event)

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

func (s *boardSnapshot) applyCollectorErrorEvent(event contracts.Event) {
	if event.Type != contracts.EventTypeSourcePoll {
		return
	}
	source := eventSourceName(event)
	if source == "" {
		return
	}
	message := event.Metadata["last_error"]
	if message == "" {
		return
	}
	if s.collectorErrors == nil {
		s.collectorErrors = make(map[string]collectorErrorBucket)
	}
	key := source + "\x00" + message
	bucket := s.collectorErrors[key]
	bucket.source = source
	bucket.message = message
	bucket.count++
	if event.Timestamp.After(bucket.lastAt) {
		bucket.lastAt = event.Timestamp
	}
	s.collectorErrors[key] = bucket
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
