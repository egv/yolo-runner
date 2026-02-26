package distributed

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestTaskGraphSnapshotPayloadRoundTripSupportsMultipleGraphs(t *testing.T) {
	payload := TaskGraphSnapshotPayload{
		SchemaVersion: InboxSchemaVersionV1,
		Graphs: []TaskGraphSnapshot{
			{
				GraphRef: "run-131",
				SourceContext: SourceContext{
					Provider:   "github",
					Repository: "egv/yolo-runner",
				},
				Nodes: []TaskGraphNode{
					{
						TaskID:   "task-1",
						GraphRef: "run-131",
						Status:   contracts.TaskStatusOpen,
						TaskRef: TaskRef{
							BackendInstance: "prod-us-1",
							BackendType:     "github",
							BackendNativeID: "147",
						},
						SourceContext: SourceContext{Provider: "github", Repository: "egv/yolo-runner"},
						WorkspaceSpec: &WorkspaceSpec{Kind: "git", RepoURL: "https://github.com/egv/yolo-runner", Ref: "main"},
						Requirements:  []TaskRequirement{{Name: "github_token", Kind: "credential_flag"}},
					},
				},
			},
			{
				GraphRef:      "run-132",
				SourceContext: SourceContext{Provider: "linear", Project: "YOLO"},
				Nodes: []TaskGraphNode{{
					TaskID:   "task-2",
					GraphRef: "run-132",
					Status:   contracts.TaskStatusInProgress,
					TaskRef: TaskRef{
						BackendInstance: "prod-us-2",
						BackendType:     "linear",
						BackendNativeID: "YOLO-12",
					},
					SourceContext: SourceContext{Provider: "linear", Project: "YOLO"},
				}},
			},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var decoded TaskGraphSnapshotPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if len(decoded.Graphs) != 2 {
		t.Fatalf("expected 2 graphs, got %d", len(decoded.Graphs))
	}
	if decoded.Graphs[0].Nodes[0].TaskRef.BackendType != "github" {
		t.Fatalf("expected github backend type, got %q", decoded.Graphs[0].Nodes[0].TaskRef.BackendType)
	}
	if got := decoded.Graphs[0].Nodes[0].Requirements[0].Name; got != "github_token" {
		t.Fatalf("expected requirement to round-trip, got %q", got)
	}
}

func TestTaskGraphSnapshotPayloadLegacyCompatibilityHydratesGraphs(t *testing.T) {
	payload := TaskGraphSnapshotPayload{
		Backend: "tk",
		RootID:  "root-1",
		TaskTree: contracts.TaskTree{
			Root: contracts.Task{ID: "root-1", Status: contracts.TaskStatusOpen},
			Tasks: map[string]contracts.Task{
				"root-1": {ID: "root-1", Status: contracts.TaskStatusOpen},
			},
		},
	}
	graphs, err := payload.NormalizeGraphs()
	if err != nil {
		t.Fatalf("normalize graphs: %v", err)
	}
	if len(graphs) != 1 {
		t.Fatalf("expected single graph from legacy payload, got %d", len(graphs))
	}
	if graphs[0].Nodes[0].GraphRef != "root-1" {
		t.Fatalf("expected graph_ref root-1, got %q", graphs[0].Nodes[0].GraphRef)
	}
	if graphs[0].Nodes[0].TaskRef.BackendType != "tk" {
		t.Fatalf("expected backend type tk, got %q", graphs[0].Nodes[0].TaskRef.BackendType)
	}
}

func TestTaskGraphSnapshotPayloadNormalizeRejectsUnknownSchemaVersion(t *testing.T) {
	payload := TaskGraphSnapshotPayload{SchemaVersion: "999", Graphs: []TaskGraphSnapshot{{GraphRef: "run-1"}}}
	if _, err := payload.NormalizeGraphs(); err == nil {
		t.Fatalf("expected unknown schema version to fail")
	}
}

func TestTaskGraphDiffPayloadRoundTripSupportsVersionAndWarnings(t *testing.T) {
	payload := TaskGraphDiffPayload{
		SchemaVersion: InboxSchemaVersionV1,
		VersionID:     42,
		Warnings:      []string{"backend linear-eu unavailable"},
		Graphs: []TaskGraphDiff{
			{
				GraphRef:      "github|repo-a|root-1",
				SourceContext: SourceContext{Provider: "github", Repository: "org/repo-a"},
				UpsertNodes: []TaskGraphNode{
					{
						TaskID:   "101",
						GraphRef: "github|repo-a|root-1",
						Status:   contracts.TaskStatusOpen,
						TaskRef: TaskRef{
							BackendInstance: "github-repo-a",
							BackendType:     "github",
							BackendNativeID: "101",
						},
						SourceContext: SourceContext{Provider: "github", Repository: "org/repo-a"},
					},
				},
				DeleteTaskIDs: []string{"102"},
				ChangedFields: []string{"status"},
			},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	var decoded TaskGraphDiffPayload
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal diff: %v", err)
	}
	if decoded.VersionID != 42 {
		t.Fatalf("expected version_id=42, got %d", decoded.VersionID)
	}
	if len(decoded.Warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(decoded.Warnings))
	}
	if len(decoded.Graphs) != 1 {
		t.Fatalf("expected one graph diff, got %d", len(decoded.Graphs))
	}
	if decoded.Graphs[0].UpsertNodes[0].TaskRef.BackendInstance != "github-repo-a" {
		t.Fatalf("expected backend instance github-repo-a, got %q", decoded.Graphs[0].UpsertNodes[0].TaskRef.BackendInstance)
	}
}

func TestMastermindTaskStatusUpdateCommandIsIdempotentByCommandID(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &fakeTaskStatusBackend{t: t}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	ackCh, unsubscribeAck, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateAck)
	if err != nil {
		t.Fatalf("subscribe ack: %v", err)
	}
	defer unsubscribeAck()

	cmd := TaskStatusUpdateCommandPayload{
		CommandID: "cmd-idempotent-1",
		Backends:  []string{"tk"},
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		Comment:   "ship it",
		AuthToken: "token",
	}
	if _, err := mastermind.PublishTaskStatusUpdateCommand(ctx, cmd); err != nil {
		t.Fatalf("publish first command: %v", err)
	}
	_ = readTaskStatusUpdateAck(t, ackCh)

	if _, err := mastermind.PublishTaskStatusUpdateCommand(ctx, cmd); err != nil {
		t.Fatalf("publish duplicate command: %v", err)
	}
	ack := readTaskStatusUpdateAck(t, ackCh)
	if !ack.Success {
		t.Fatalf("expected duplicate command to return success ack")
	}
	if calls, _ := backend.callsFor("task-1"); calls != 1 {
		t.Fatalf("expected idempotent command to write status once, got %d", calls)
	}
}

func TestMastermindTaskStatusDuplicateCommandIDSkipsAuthRejection(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &fakeTaskStatusBackend{t: t}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	ackCh, unsubscribeAck, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateAck)
	if err != nil {
		t.Fatalf("subscribe ack: %v", err)
	}
	defer unsubscribeAck()
	rejectCh, unsubscribeReject, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateReject)
	if err != nil {
		t.Fatalf("subscribe reject: %v", err)
	}
	defer unsubscribeReject()

	cmd := TaskStatusUpdateCommandPayload{
		CommandID: "cmd-idempotent-auth-1",
		Backends:  []string{"tk"},
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		Comment:   "ship it",
		AuthToken: "token",
	}
	if _, err := mastermind.PublishTaskStatusUpdateCommand(ctx, cmd); err != nil {
		t.Fatalf("publish first command: %v", err)
	}
	_ = readTaskStatusUpdateAck(t, ackCh)

	cmd.AuthToken = "wrong-token"
	if _, err := mastermind.PublishTaskStatusUpdateCommand(ctx, cmd); err != nil {
		t.Fatalf("publish duplicate command: %v", err)
	}
	ack := readTaskStatusUpdateAck(t, ackCh)
	if !ack.Success {
		t.Fatalf("expected duplicate command to keep success ack")
	}
	if calls, _ := backend.callsFor("task-1"); calls != 1 {
		t.Fatalf("expected idempotent command to write status once, got %d", calls)
	}
	select {
	case reject := <-rejectCh:
		t.Fatalf("did not expect reject event for duplicate command, got type=%q", reject.Type)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestMastermindPublishesFailureAckForRejectedStatusCommand(t *testing.T) {
	bus := NewMemoryBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &fakeTaskStatusBackend{t: t}
	subjects := DefaultEventSubjects("unit")
	mastermind := NewMastermind(MastermindOptions{
		ID:             "mastermind",
		Bus:            bus,
		Subjects:       subjects,
		RegistryTTL:    2 * time.Second,
		RequestTimeout: 2 * time.Second,
		StatusUpdateBackends: map[string]TaskStatusWriter{
			"tk": backend,
		},
		StatusUpdateAuthToken: "token",
	})
	if err := mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}
	ackCh, unsubscribeAck, err := bus.Subscribe(ctx, subjects.TaskStatusUpdateAck)
	if err != nil {
		t.Fatalf("subscribe ack: %v", err)
	}
	defer unsubscribeAck()

	_, err = mastermind.PublishTaskStatusUpdateCommand(ctx, TaskStatusUpdateCommandPayload{
		CommandID: "cmd-auth-fail-1",
		Backends:  []string{"tk"},
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		AuthToken: "wrong-token",
	})
	if err != nil {
		t.Fatalf("publish command: %v", err)
	}
	ack := readTaskStatusUpdateAck(t, ackCh)
	if ack.Success {
		t.Fatalf("expected failure ack for rejected command")
	}
	if ack.Reason == "" {
		t.Fatalf("expected failure reason in ack")
	}
}
