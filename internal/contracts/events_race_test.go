package contracts

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// Producers may keep mutating the metadata map they attached to an event
// after Emit returns; buffered sinks must marshal a private copy obtained via
// WithClonedMetadata. Run with -race to exercise the guarantee.
func TestWithClonedMetadataIsSafeAgainstProducerMutation(t *testing.T) {
	metadata := map[string]string{"sequence": "0"}
	event := Event{
		Type:      EventTypeAgentText,
		TaskID:    "task-1",
		Message:   "line",
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	}

	queued := event.WithClonedMetadata()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			metadata[fmt.Sprintf("key-%d", i%7)] = fmt.Sprint(i)
		}
	}()

	for i := 0; i < 1000; i++ {
		if _, err := MarshalEventJSONL(queued); err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	wg.Wait()

	if queued.Metadata["sequence"] != "0" || len(queued.Metadata) != 1 {
		t.Fatalf("clone must be unaffected by producer mutation: %#v", queued.Metadata)
	}
}
