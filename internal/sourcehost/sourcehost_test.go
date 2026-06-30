package sourcehost_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/sourcehost"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunPollsAndConsumesResultsThroughWorkqueue(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())

	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	src := &fakeSourcehostSource{
		name: "fake-source",
		pollSubmissions: []workqueue.Submission{
			{
				Kind:           workitem.KindPreflight,
				Source:         "fake-source",
				SourceRef:      "TASK-1",
				IdempotencyKey: "fake-source/TASK-1/preflight",
				Preset:         "linux",
				Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
			},
		},
		followUps: []workqueue.Submission{
			{
				Kind:           workitem.KindImplement,
				Source:         "fake-source",
				SourceRef:      "TASK-1",
				IdempotencyKey: "fake-source/TASK-1/implement",
				Preset:         "linux",
				Payload:        json.RawMessage(`{"task_id":"TASK-1","stage":"implement"}`),
			},
		},
	}

	if err := sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true}); err != nil {
		t.Fatalf("Run(poll) error = %v", err)
	}

	polled, err := store.Claim("runner-a", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(polled) error = %v", err)
	}
	if polled == nil {
		t.Fatalf("Claim(polled) returned nil")
	}
	if polled.IdempotencyKey != "fake-source/TASK-1/preflight" {
		t.Fatalf("polled idempotency key = %q, want preflight key", polled.IdempotencyKey)
	}

	if err := store.Complete(polled.ID, workqueue.Result{
		Payload: json.RawMessage(`{"verdict":"ready"}`),
	}); err != nil {
		t.Fatalf("Complete(polled) error = %v", err)
	}

	if err := sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true}); err != nil {
		t.Fatalf("Run(consume) error = %v", err)
	}

	if len(src.handled) != 1 {
		t.Fatalf("HandleResult calls = %d, want 1", len(src.handled))
	}
	if src.handled[0].item.ID != polled.ID {
		t.Fatalf("handled item ID = %q, want %q", src.handled[0].item.ID, polled.ID)
	}
	if src.handled[0].result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("handled result status = %q, want completed", src.handled[0].result.Status)
	}

	unconsumed, err := store.ListUnconsumedResults("fake-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(unconsumed) != 0 {
		t.Fatalf("unconsumed results = %d, want 0", len(unconsumed))
	}

	followUp, err := store.Claim("runner-b", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(follow-up) error = %v", err)
	}
	if followUp == nil {
		t.Fatalf("Claim(follow-up) returned nil")
	}
	if followUp.Kind != workitem.KindImplement {
		t.Fatalf("follow-up kind = %q, want implement", followUp.Kind)
	}
	if followUp.IdempotencyKey != "fake-source/TASK-1/implement" {
		t.Fatalf("follow-up idempotency key = %q, want implement key", followUp.IdempotencyKey)
	}
}

func TestRunEmitsAgentHeartbeatAfterSuccessfulIteration(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())

	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	sink := &sourcehostRecordingSink{}
	src := &fakeSourcehostSource{name: "fake-source"}
	if err := sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true, ProcID: "sourcehost-1", EventSink: sink}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	event, ok := sourcehostEventByType(sink.events, contracts.EventTypeAgentHeartbeat)
	if !ok {
		t.Fatalf("missing %q event in %#v", contracts.EventTypeAgentHeartbeat, sink.events)
	}
	if event.Metadata["component"] != "sourcehost" || event.Metadata["source"] != "fake-source" || event.Metadata["proc"] != "sourcehost-1" {
		t.Fatalf("heartbeat metadata = %#v", event.Metadata)
	}
}

func TestRunEmitsAgentProgressWhenReapingStaleItems(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())

	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPreflight,
		Source:         "fake-source",
		SourceRef:      "TASK-STALE",
		IdempotencyKey: "fake-source/TASK-STALE/preflight",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-STALE"}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"linux"}, time.Nanosecond)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil || claimed.ID != item.ID {
		t.Fatalf("claimed item = %#v, want %q", claimed, item.ID)
	}
	time.Sleep(time.Millisecond)

	sink := &sourcehostRecordingSink{}
	src := &fakeSourcehostSource{name: "fake-source"}
	if err := sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true, ProcID: "sourcehost-1", EventSink: sink}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	event, ok := sourcehostEventByType(sink.events, contracts.EventTypeAgentProgress)
	if !ok {
		t.Fatalf("missing %q event in %#v", contracts.EventTypeAgentProgress, sink.events)
	}
	if event.Metadata["reaped"] != "1" {
		t.Fatalf("progress metadata = %#v, want reaped=1", event.Metadata)
	}
}

func TestRunEmitsAgentProgressWarningAfterFailedIteration(t *testing.T) {
	ctx := context.Background()
	t.Setenv("HOME", t.TempDir())

	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	sink := &sourcehostRecordingSink{}
	src := &fakeSourcehostSource{name: "fake-source", pollErr: errors.New("poll failed")}
	err = sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true, ProcID: "sourcehost-1", EventSink: sink})
	if err == nil {
		t.Fatalf("Run() error = nil, want poll failure")
	}

	event, ok := sourcehostEventByType(sink.events, contracts.EventTypeAgentProgress)
	if !ok {
		t.Fatalf("missing %q event in %#v", contracts.EventTypeAgentProgress, sink.events)
	}
	if event.Metadata["level"] != "warning" || event.Metadata["source"] != "fake-source" {
		t.Fatalf("warning metadata = %#v", event.Metadata)
	}
}

type fakeSourcehostSource struct {
	name            string
	pollSubmissions []workqueue.Submission
	pollErr         error
	followUps       []workqueue.Submission
	handled         []handledSourcehostResult
}

type handledSourcehostResult struct {
	item   workitem.Item
	result workqueue.Result
}

func (s *fakeSourcehostSource) Name() string {
	return s.name
}

func (s *fakeSourcehostSource) Poll(context.Context) ([]workqueue.Submission, error) {
	if s.pollErr != nil {
		return nil, s.pollErr
	}
	return s.pollSubmissions, nil
}

func (s *fakeSourcehostSource) HandleResult(_ context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	s.handled = append(s.handled, handledSourcehostResult{item: item, result: result})
	return s.followUps, nil
}

type sourcehostRecordingSink struct {
	events []contracts.Event
}

func (s *sourcehostRecordingSink) Emit(_ context.Context, event contracts.Event) error {
	s.events = append(s.events, event)
	return nil
}

func sourcehostEventByType(events []contracts.Event, eventType contracts.EventType) (contracts.Event, bool) {
	for _, event := range events {
		if event.Type == eventType {
			return event, true
		}
	}
	return contracts.Event{}, false
}
