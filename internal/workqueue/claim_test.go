package workqueue

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestClaimAndHeartbeatAreAtomicAcrossConnections(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")

	first, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("Close(first) error = %v", err)
		}
	})

	second, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close(second) error = %v", err)
		}
	})

	createdAt := time.Now().UTC().Add(-time.Hour)
	claimableIDs := []string{"claimable-high-old", "claimable-high-new"}
	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "claimable-low-old",
		preset:    "linux",
		priority:  1,
		state:     "pending",
		createdAt: createdAt,
	})
	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "claimable-high-new",
		preset:    "linux",
		priority:  10,
		state:     "pending",
		createdAt: createdAt.Add(2 * time.Second),
	})
	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "claimable-high-old",
		preset:    "linux",
		priority:  10,
		state:     "pending",
		createdAt: createdAt.Add(time.Second),
	})

	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "done-dependency",
		preset:    "linux",
		priority:  100,
		state:     "done",
		createdAt: createdAt,
	})
	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "claimable-with-done-dep",
		preset:    "linux",
		priority:  9,
		state:     "pending",
		createdAt: createdAt,
	})
	insertWorkQueueDependency(t, first.db, "claimable-with-done-dep", "done-dependency")
	claimableIDs = append(claimableIDs, "claimable-with-done-dep", "claimable-low-old")

	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "blocking-dependency",
		preset:    "deps-only",
		priority:  100,
		state:     "pending",
		createdAt: createdAt,
	})
	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "blocked-by-open-dep",
		preset:    "linux",
		priority:  100,
		state:     "pending",
		createdAt: createdAt,
	})
	insertWorkQueueDependency(t, first.db, "blocked-by-open-dep", "blocking-dependency")

	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "future-item",
		preset:    "linux",
		priority:  100,
		state:     "pending",
		notBefore: time.Now().UTC().Add(time.Hour),
		createdAt: createdAt,
	})
	insertWorkQueueItem(t, first.db, testQueueItem{
		id:        "wrong-preset",
		preset:    "darwin",
		priority:  100,
		state:     "pending",
		createdAt: createdAt,
	})

	firstClaim, err := first.Claim("runner-a", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if firstClaim == nil {
		t.Fatalf("Claim(first) returned nil, want claimable-high-old")
	}
	if firstClaim.ID != "claimable-high-old" {
		t.Fatalf("Claim(first).ID = %q, want %q", firstClaim.ID, "claimable-high-old")
	}
	if firstClaim.State != "claimed" {
		t.Fatalf("Claim(first).State = %q, want claimed", firstClaim.State)
	}
	if firstClaim.ClaimedBy != "runner-a" {
		t.Fatalf("Claim(first).ClaimedBy = %q, want runner-a", firstClaim.ClaimedBy)
	}
	if firstClaim.Attempt != 1 {
		t.Fatalf("Claim(first).Attempt = %d, want 1", firstClaim.Attempt)
	}
	if firstClaim.HeartbeatAt.IsZero() || firstClaim.LeaseExpiresAt.IsZero() {
		t.Fatalf("Claim(first) did not set lease timestamps: %#v", firstClaim)
	}

	forcedHeartbeat := time.Now().UTC().Add(-10 * time.Minute)
	forcedLease := forcedHeartbeat.Add(time.Minute)
	forceWorkQueueLease(t, first.db, firstClaim.ID, forcedHeartbeat, forcedLease)
	if err := first.Heartbeat(firstClaim.ID, "runner-a"); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	heartbeatAt, leaseExpiresAt := readWorkQueueLease(t, first.db, firstClaim.ID)
	if !heartbeatAt.After(forcedHeartbeat) {
		t.Fatalf("heartbeat_at = %v, want after %v", heartbeatAt, forcedHeartbeat)
	}
	if !leaseExpiresAt.After(forcedLease) {
		t.Fatalf("lease_expires_at = %v, want after %v", leaseExpiresAt, forcedLease)
	}

	claimed, claimErrs := claimConcurrently(t, []*Store{first, second}, []string{"linux"}, time.Minute)
	for err := range claimErrs {
		if err != nil {
			t.Fatalf("concurrent Claim() error = %v", err)
		}
	}

	seen := map[string]bool{firstClaim.ID: true}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("item %q was claimed more than once", id)
		}
		seen[id] = true
	}
	for _, id := range claimableIDs {
		if !seen[id] {
			t.Fatalf("item %q was not claimed; claimed IDs = %#v", id, seen)
		}
	}
	if len(seen) != len(claimableIDs) {
		t.Fatalf("claimed unexpected item IDs: got %#v, want only %#v", seen, claimableIDs)
	}
	for _, id := range []string{"blocked-by-open-dep", "future-item", "wrong-preset"} {
		if seen[id] {
			t.Fatalf("item %q should not have been claimable", id)
		}
	}

	assertWorkQueueState(t, first.db, "blocked-by-open-dep", "pending")
	noItem, err := first.Claim("runner-c", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(blocked only) error = %v", err)
	}
	if noItem != nil {
		t.Fatalf("Claim(blocked only) = %q, want nil", noItem.ID)
	}

	updateWorkQueueState(t, first.db, "blocking-dependency", "done")
	unblocked, err := first.Claim("runner-c", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(unblocked) error = %v", err)
	}
	if unblocked == nil {
		t.Fatalf("Claim(unblocked) returned nil, want blocked-by-open-dep")
	}
	if unblocked.ID != "blocked-by-open-dep" {
		t.Fatalf("Claim(unblocked).ID = %q, want blocked-by-open-dep", unblocked.ID)
	}
}

func TestClaimForSourceRefLeavesOtherRunnableItemsPending(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	createdAt := time.Now().UTC()
	for _, item := range []testQueueItem{
		{id: "other-pr", priority: 10, state: "pending", createdAt: createdAt},
		{id: "target-pr", priority: 0, state: "pending", createdAt: createdAt.Add(time.Second)},
	} {
		insertWorkQueueItem(t, store.db, item)
	}
	if _, err := store.db.Exec("UPDATE work_items SET source_ref = ? WHERE id = ?", "pr:14330209", "target-pr"); err != nil {
		t.Fatalf("set target source ref: %v", err)
	}

	claimed, err := store.ClaimForSourceRef("runner", []string{"linux"}, "pr:14330209", time.Minute)
	if err != nil {
		t.Fatalf("ClaimForSourceRef() error = %v", err)
	}
	if claimed == nil || claimed.ID != "target-pr" {
		t.Fatalf("ClaimForSourceRef() = %#v, want target-pr", claimed)
	}
	assertWorkQueueState(t, store.db, "other-pr", "pending")
}

type testQueueItem struct {
	id        string
	preset    string
	priority  int
	state     string
	notBefore time.Time
	createdAt time.Time
}

func insertWorkQueueItem(t *testing.T, db *sql.DB, item testQueueItem) {
	t.Helper()

	if item.createdAt.IsZero() {
		item.createdAt = time.Now().UTC()
	}
	if item.state == "" {
		item.state = "pending"
	}
	if item.preset == "" {
		item.preset = "linux"
	}

	_, err := db.Exec(`
INSERT INTO work_items (
	id, kind, source, source_ref, idempotency_key, preset, priority, payload,
	state, attempt, max_attempts, not_before, claimed_by, lease_expires_at,
	heartbeat_at, created_at, updated_at
) VALUES (?, 'implement', 'test-source', ?, ?, ?, ?, '{}', ?, 0, 3, ?, '', '', '', ?, ?)`,
		item.id,
		item.id,
		"test-key/"+item.id,
		item.preset,
		item.priority,
		item.state,
		formatTestQueueTime(item.notBefore),
		formatTestQueueTime(item.createdAt),
		formatTestQueueTime(item.createdAt),
	)
	if err != nil {
		t.Fatalf("insert work item %q: %v", item.id, err)
	}
}

func insertWorkQueueDependency(t *testing.T, db *sql.DB, itemID string, dependsOn string) {
	t.Helper()

	if _, err := db.Exec(
		"INSERT INTO item_deps (item_id, depends_on) VALUES (?, ?)",
		itemID,
		dependsOn,
	); err != nil {
		t.Fatalf("insert dependency %q -> %q: %v", itemID, dependsOn, err)
	}
}

func claimConcurrently(t *testing.T, stores []*Store, presets []string, leaseTTL time.Duration) (<-chan string, <-chan error) {
	t.Helper()

	claimed := make(chan string, 32)
	errs := make(chan error, len(stores))
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i, store := range stores {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			<-start
			runnerID := fmt.Sprintf("runner-%d", i+1)
			for {
				item, err := store.Claim(runnerID, presets, leaseTTL)
				if err != nil {
					errs <- err
					return
				}
				if item == nil {
					errs <- nil
					return
				}
				if item.State != "claimed" {
					errs <- fmt.Errorf("claimed item %q state = %q, want claimed", item.ID, item.State)
					return
				}
				if item.ClaimedBy != runnerID {
					errs <- fmt.Errorf("claimed item %q runner = %q, want %q", item.ID, item.ClaimedBy, runnerID)
					return
				}
				claimed <- item.ID
			}
		}(i, store)
	}

	close(start)
	go func() {
		wg.Wait()
		close(claimed)
		close(errs)
	}()

	return claimed, errs
}

func forceWorkQueueLease(t *testing.T, db *sql.DB, itemID string, heartbeatAt time.Time, leaseExpiresAt time.Time) {
	t.Helper()

	result, err := db.Exec(`
UPDATE work_items
SET heartbeat_at = ?, lease_expires_at = ?
WHERE id = ?`,
		formatTestQueueTime(heartbeatAt),
		formatTestQueueTime(leaseExpiresAt),
		itemID,
	)
	if err != nil {
		t.Fatalf("force lease for %q: %v", itemID, err)
	}
	assertRowsAffected(t, result, "force lease")
}

func readWorkQueueLease(t *testing.T, db *sql.DB, itemID string) (time.Time, time.Time) {
	t.Helper()

	var heartbeatAt string
	var leaseExpiresAt string
	if err := db.QueryRow(
		"SELECT heartbeat_at, lease_expires_at FROM work_items WHERE id = ?",
		itemID,
	).Scan(&heartbeatAt, &leaseExpiresAt); err != nil {
		t.Fatalf("read lease for %q: %v", itemID, err)
	}
	return parseTestQueueTime(t, heartbeatAt), parseTestQueueTime(t, leaseExpiresAt)
}

func updateWorkQueueState(t *testing.T, db *sql.DB, itemID string, state string) {
	t.Helper()

	result, err := db.Exec("UPDATE work_items SET state = ? WHERE id = ?", state, itemID)
	if err != nil {
		t.Fatalf("update item %q state to %q: %v", itemID, state, err)
	}
	assertRowsAffected(t, result, "update state")
}

func assertWorkQueueState(t *testing.T, db *sql.DB, itemID string, want string) {
	t.Helper()

	var got string
	if err := db.QueryRow("SELECT state FROM work_items WHERE id = ?", itemID).Scan(&got); err != nil {
		t.Fatalf("read item %q state: %v", itemID, err)
	}
	if got != want {
		t.Fatalf("item %q state = %q, want %q", itemID, got, want)
	}
}

func assertRowsAffected(t *testing.T, result sql.Result, action string) {
	t.Helper()

	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("%s rows affected: %v", action, err)
	}
	if affected != 1 {
		t.Fatalf("%s affected %d rows, want 1", action, affected)
	}
}

func formatTestQueueTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTestQueueTime(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse queue time %q: %v", value, err)
	}
	return parsed
}
