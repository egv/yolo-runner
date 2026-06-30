package workqueue

import (
	"reflect"
	"sort"
	"testing"
)

func TestListSourcesAndItemStateCountsAggregateWorkItems(t *testing.T) {
	store := openEnqueueTestStore(t)

	seedWorkItemState(t, store, "item-1", "source-a", "open")
	seedWorkItemState(t, store, "item-2", "source-a", "open")
	seedWorkItemState(t, store, "item-3", "source-a", "claimed")
	seedWorkItemState(t, store, "item-4", "source-b", "open")
	seedWorkItemState(t, store, "item-5", "source-b", "done")
	seedWorkItemState(t, store, "item-6", "source-c", "failed")

	gotSources, err := store.ListSources()
	if err != nil {
		t.Fatalf("ListSources() error = %v", err)
	}
	sort.Slice(gotSources, func(i, j int) bool {
		if gotSources[i].Source != gotSources[j].Source {
			return gotSources[i].Source < gotSources[j].Source
		}
		return gotSources[i].State < gotSources[j].State
	})
	wantSources := []SourceRow{
		{Source: "source-a", State: "claimed", Count: 1},
		{Source: "source-a", State: "open", Count: 2},
		{Source: "source-b", State: "done", Count: 1},
		{Source: "source-b", State: "open", Count: 1},
		{Source: "source-c", State: "failed", Count: 1},
	}
	if !reflect.DeepEqual(gotSources, wantSources) {
		t.Fatalf("ListSources() = %#v, want %#v", gotSources, wantSources)
	}

	gotCounts, err := store.ItemStateCounts()
	if err != nil {
		t.Fatalf("ItemStateCounts() error = %v", err)
	}
	wantCounts := map[string]int{
		"claimed": 1,
		"done":    1,
		"failed":  1,
		"open":    3,
	}
	if !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Fatalf("ItemStateCounts() = %#v, want %#v", gotCounts, wantCounts)
	}
}

func seedWorkItemState(t *testing.T, store *Store, id string, source string, state string) {
	t.Helper()

	_, err := store.db.Exec(`
INSERT INTO work_items (
	id,
	kind,
	source,
	source_ref,
	idempotency_key,
	preset,
	priority,
	payload,
	state,
	attempt,
	max_attempts,
	not_before,
	claimed_by,
	lease_expires_at,
	heartbeat_at,
	created_at,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		"task",
		source,
		id,
		"idempotency-"+id,
		"default",
		1,
		`{"hello":"world"}`,
		state,
		0,
		3,
		"",
		"",
		"",
		"",
		"2026-06-29T00:00:00Z",
		"2026-06-29T00:00:00Z",
	)
	if err != nil {
		t.Fatalf("insert %s/%s work item: %v", source, state, err)
	}
}
