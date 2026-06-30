package workqueue

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestListItemsFiltersOrderingAndLimit(t *testing.T) {
	store := openInspectItemsTestStore(t)

	seedInspectItem(t, store, "item-pending-a", "github", "pending", "fast", workitem.KindImplement, "", 20, "2026-06-30T08:00:00Z")
	seedInspectItem(t, store, "item-pending-b", "github", "pending", "slow", workitem.KindReview, "runner-1", 20, "2026-06-30T08:01:00Z")
	seedInspectItem(t, store, "item-pending-c", "startrek", "pending", "fast", workitem.KindImplement, "runner-2", 10, "2026-06-30T07:59:00Z")
	seedInspectItem(t, store, "item-running-a", "github", "running", "fast", workitem.KindImplement, "runner-1", 50, "2026-06-30T08:02:00Z")
	seedInspectItem(t, store, "item-done-a", "startrek", "done", "slow", workitem.KindReview, "", 30, "2026-06-30T08:03:00Z")

	tests := []struct {
		name   string
		filter ListItemsFilter
		want   []string
	}{
		{
			name:   "source",
			filter: ListItemsFilter{Source: "github"},
			want:   []string{"item-pending-a", "item-pending-b", "item-running-a"},
		},
		{
			name:   "state",
			filter: ListItemsFilter{State: "pending"},
			want:   []string{"item-pending-a", "item-pending-b", "item-pending-c"},
		},
		{
			name:   "preset",
			filter: ListItemsFilter{Preset: "slow"},
			want:   []string{"item-done-a", "item-pending-b"},
		},
		{
			name:   "kind",
			filter: ListItemsFilter{Kind: string(workitem.KindReview)},
			want:   []string{"item-done-a", "item-pending-b"},
		},
		{
			name:   "claimed by",
			filter: ListItemsFilter{ClaimedBy: "runner-1"},
			want:   []string{"item-pending-b", "item-running-a"},
		},
		{
			name:   "combined",
			filter: ListItemsFilter{Source: "github", State: "pending", Preset: "slow", Kind: string(workitem.KindReview), ClaimedBy: "runner-1"},
			want:   []string{"item-pending-b"},
		},
		{
			name:   "explicit limit",
			filter: ListItemsFilter{Limit: 2},
			want:   []string{"item-done-a", "item-pending-a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := store.ListItems(tt.filter)
			if err != nil {
				t.Fatalf("ListItems() error = %v", err)
			}
			if got := inspectItemIDs(items); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ListItems() IDs = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestListItemsDefaultLimitCapsAtFiveHundred(t *testing.T) {
	store := openInspectItemsTestStore(t)

	for i := 0; i < 501; i++ {
		seedInspectItem(t, store, fmt.Sprintf("item-%03d", i), "github", "pending", "fast", workitem.KindImplement, "", i, "2026-06-30T08:00:00Z")
	}

	items, err := store.ListItems(ListItemsFilter{})
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	if len(items) != 500 {
		t.Fatalf("ListItems() returned %d items, want default cap 500", len(items))
	}
}

func openInspectItemsTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func seedInspectItem(t *testing.T, store *Store, id, source, state, preset string, kind workitem.Kind, claimedBy string, priority int, createdAt string) {
	t.Helper()

	payload := json.RawMessage(`{"id":"` + id + `"}`)
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
		string(kind),
		source,
		id+"-ref",
		id+"-key",
		preset,
		priority,
		string(payload),
		state,
		0,
		3,
		"",
		claimedBy,
		"",
		"",
		createdAt,
		createdAt,
	)
	if err != nil {
		t.Fatalf("seed item %q: %v", id, err)
	}
}

func inspectItemIDs(items []workitem.Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
