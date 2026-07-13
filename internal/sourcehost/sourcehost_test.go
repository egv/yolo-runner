package sourcehost_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
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

func TestRunConsumesLaterResultsWhenEarlierWritebackFails(t *testing.T) {
	ctx := context.Background()
	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	var itemIDs []string
	for _, ref := range []string{"PR-BLOCKED", "PR-READY"} {
		item, submitErr := store.Submit(workitem.Submission{
			Kind:           workitem.KindPRReview,
			Source:         "fake-source",
			SourceRef:      ref,
			IdempotencyKey: "fake-source/" + ref,
			Preset:         "arcpr",
			Payload:        json.RawMessage(`{"pr_id":"42"}`),
		})
		if submitErr != nil {
			t.Fatalf("Submit(%s) error = %v", ref, submitErr)
		}
		claimed, claimErr := store.Claim("runner", []string{"arcpr"}, time.Minute)
		if claimErr != nil {
			t.Fatalf("Claim(%s) error = %v", ref, claimErr)
		}
		if claimed == nil || claimed.ID != item.ID {
			t.Fatalf("Claim(%s) = %#v, want %q", ref, claimed, item.ID)
		}
		if completeErr := store.Complete(item.ID, workqueue.Result{Payload: json.RawMessage(`{}`)}); completeErr != nil {
			t.Fatalf("Complete(%s) error = %v", ref, completeErr)
		}
		itemIDs = append(itemIDs, item.ID)
	}

	src := &fakeSourcehostSource{
		name: "fake-source",
		handleErrs: map[string]error{itemIDs[0]: errors.New("writeback blocked")},
		pollSubmissions: []workqueue.Submission{
			{
				Kind:           workitem.KindPRReview,
				Source:         "fake-source",
				SourceRef:      "PR-DISCOVERED",
				IdempotencyKey: "fake-source/PR-DISCOVERED",
				Preset:         "arcpr",
				Payload:        json.RawMessage(`{"pr_id":"43"}`),
			},
		},
	}
	err = sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true})
	if err == nil || !strings.Contains(err.Error(), "writeback blocked") {
		t.Fatalf("Run() error = %v, want writeback failure", err)
	}
	if len(src.handled) != 2 {
		t.Fatalf("HandleResult calls = %d, want 2", len(src.handled))
	}
	unconsumed, listErr := store.ListUnconsumedResults("fake-source")
	if listErr != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", listErr)
	}
	if len(unconsumed) != 1 || unconsumed[0].Item.ID != itemIDs[0] {
		t.Fatalf("unconsumed results = %#v, want only %q", unconsumed, itemIDs[0])
	}
	discovered, claimErr := store.Claim("runner-discovered", []string{"arcpr"}, time.Minute)
	if claimErr != nil {
		t.Fatalf("Claim(discovered work) error = %v", claimErr)
	}
	if discovered == nil || discovered.IdempotencyKey != "fake-source/PR-DISCOVERED" {
		t.Fatalf("discovered work = %#v, want fake-source/PR-DISCOVERED", discovered)
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

func TestRunEmitsSourcePollAndHeartbeatWithIdentityAndLastError(t *testing.T) {
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

	if _, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPreflight,
		Source:         "fake-source",
		SourceRef:      "TASK-1",
		IdempotencyKey: "fake-source/TASK-1/preflight",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
	}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	sink := &sourcehostRecordingSink{}
	src := &fakeSourcehostSource{name: "fake-source", pollErr: errors.New("startrek token missing")}
	err = sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true, ProcID: "sourcehost-1", EventSink: sink})
	if err == nil {
		t.Fatalf("Run() error = nil, want poll failure")
	}

	poll, ok := sourcehostEventByType(sink.events, contracts.EventTypeSourcePoll)
	if !ok {
		t.Fatalf("missing %q event in %#v", contracts.EventTypeSourcePoll, sink.events)
	}
	if poll.Source != "fake-source" || poll.Proc != "sourcehost-1" || poll.Metadata["component"] != "sourcehost" {
		t.Fatalf("source_poll identity = source %q proc %q metadata %#v", poll.Source, poll.Proc, poll.Metadata)
	}
	if poll.Metadata["source"] != "fake-source" || poll.Metadata["proc"] != "sourcehost-1" {
		t.Fatalf("source_poll metadata = %#v", poll.Metadata)
	}
	if poll.Metadata["last_error"] != `poll source "fake-source": startrek token missing` {
		t.Fatalf("source_poll last_error = %q", poll.Metadata["last_error"])
	}
	if poll.Metadata["state_pending"] != "1" {
		t.Fatalf("source_poll state counts = %#v, want state_pending=1", poll.Metadata)
	}

	heartbeat, ok := sourcehostEventByType(sink.events, contracts.EventTypeSourceHeartbeat)
	if !ok {
		t.Fatalf("missing %q event in %#v", contracts.EventTypeSourceHeartbeat, sink.events)
	}
	if heartbeat.Source != "fake-source" || heartbeat.Proc != "sourcehost-1" || heartbeat.Metadata["component"] != "sourcehost" {
		t.Fatalf("source_heartbeat identity = source %q proc %q metadata %#v", heartbeat.Source, heartbeat.Proc, heartbeat.Metadata)
	}
}

func TestRunEmitsSourcePollStateCountsScopedToSource(t *testing.T) {
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

	for _, submission := range []workitem.Submission{
		{
			Kind:           workitem.KindPreflight,
			Source:         "fake-source",
			SourceRef:      "TASK-1",
			IdempotencyKey: "fake-source/TASK-1/preflight",
			Preset:         "linux",
			Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
		},
		{
			Kind:           workitem.KindPreflight,
			Source:         "other-source",
			SourceRef:      "TASK-2",
			IdempotencyKey: "other-source/TASK-2/preflight",
			Preset:         "linux",
			Payload:        json.RawMessage(`{"task_id":"TASK-2"}`),
		},
	} {
		if _, err := store.Submit(submission); err != nil {
			t.Fatalf("Submit(%s) error = %v", submission.Source, err)
		}
	}

	sink := &sourcehostRecordingSink{}
	src := &fakeSourcehostSource{name: "fake-source"}
	if err := sourcehost.Run(ctx, src, store, sourcehost.Options{Once: true, ProcID: "sourcehost-1", EventSink: sink}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	poll, ok := sourcehostEventByType(sink.events, contracts.EventTypeSourcePoll)
	if !ok {
		t.Fatalf("missing %q event in %#v", contracts.EventTypeSourcePoll, sink.events)
	}
	if poll.Metadata["state_pending"] != "1" {
		t.Fatalf("source_poll state counts = %#v, want state_pending=1 scoped to fake-source", poll.Metadata)
	}
}

func TestRunSuppressesConsecutiveIdenticalSourcePollErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
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
	src := &fakeSourcehostSource{
		name:    "fake-source",
		pollErr: errors.New("startrek token missing"),
		onPoll: func(count int) {
			if count == 3 {
				cancel()
			}
		},
	}
	err = sourcehost.Run(ctx, src, store, sourcehost.Options{
		PollInterval:           time.Nanosecond,
		MaxConsecutiveFailures: -1,
		ProcID:                 "sourcehost-1",
		EventSink:              sink,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}

	polls := sourcehostEventsByType(sink.events, contracts.EventTypeSourcePoll)
	if len(polls) != 1 {
		t.Fatalf("source_poll events = %d, want 1: %#v", len(polls), polls)
	}
	if polls[0].Metadata["last_error"] != `poll source "fake-source": startrek token missing` {
		t.Fatalf("source_poll last_error = %q", polls[0].Metadata["last_error"])
	}
	warnings := sourcehostWarningEvents(sink.events)
	if len(warnings) != 1 {
		t.Fatalf("warning events = %d, want 1: %#v", len(warnings), warnings)
	}
	if warnings[0].Message != `poll source "fake-source": startrek token missing` {
		t.Fatalf("warning message = %q", warnings[0].Message)
	}
	heartbeats := sourcehostEventsByType(sink.events, contracts.EventTypeSourceHeartbeat)
	if len(heartbeats) != 3 {
		t.Fatalf("source_heartbeat events = %d, want 3: %#v", len(heartbeats), heartbeats)
	}
}

type fakeSourcehostSource struct {
	name            string
	pollSubmissions []workqueue.Submission
	pollErr         error
	followUps       []workqueue.Submission
	handled         []handledSourcehostResult
	polls           int
	onPoll          func(int)
	handleErrs      map[string]error
}

type handledSourcehostResult struct {
	item   workitem.Item
	result workqueue.Result
}

func (s *fakeSourcehostSource) Name() string {
	return s.name
}

func (s *fakeSourcehostSource) Poll(context.Context) ([]workqueue.Submission, error) {
	s.polls++
	if s.onPoll != nil {
		s.onPoll(s.polls)
	}
	if s.pollErr != nil {
		return nil, s.pollErr
	}
	return s.pollSubmissions, nil
}

func (s *fakeSourcehostSource) HandleResult(_ context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	s.handled = append(s.handled, handledSourcehostResult{item: item, result: result})
	if err := s.handleErrs[item.ID]; err != nil {
		return nil, err
	}
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

func sourcehostEventsByType(events []contracts.Event, eventType contracts.EventType) []contracts.Event {
	var matches []contracts.Event
	for _, event := range events {
		if event.Type == eventType {
			matches = append(matches, event)
		}
	}
	return matches
}

func sourcehostWarningEvents(events []contracts.Event) []contracts.Event {
	var matches []contracts.Event
	for _, event := range events {
		if event.Type == contracts.EventTypeAgentProgress && event.Metadata["level"] == "warning" {
			matches = append(matches, event)
		}
	}
	return matches
}
