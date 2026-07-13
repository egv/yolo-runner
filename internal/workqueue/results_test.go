package workqueue

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestCompleteListAndMarkConsumedEnqueuesFollowUpsTransactionally(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := Open(dbPath)
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
		Source:         "source-a",
		SourceRef:      "TASK-1",
		IdempotencyKey: "source-a/TASK-1/preflight",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
		MaxAttempts:    2,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	duplicate, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindPreflight,
		Source:         "source-a",
		SourceRef:      "TASK-1",
		IdempotencyKey: "source-a/TASK-1/preflight",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
	})
	if err != nil {
		t.Fatalf("Submit(duplicate) error = %v", err)
	}
	if duplicate.ID != item.ID {
		t.Fatalf("duplicate Submit ID = %q, want existing %q", duplicate.ID, item.ID)
	}

	if err := store.Complete(item.ID, Result{
		Payload: json.RawMessage(`{"verdict":"ready"}`),
		LogPath: "/tmp/preflight.log",
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	results, err := store.ListUnconsumedResults("source-a")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	if results[0].Item.ID != item.ID {
		t.Fatalf("listed item ID = %q, want %q", results[0].Item.ID, item.ID)
	}
	if results[0].Result.Status != ResultStatusCompleted {
		t.Fatalf("listed status = %q, want completed", results[0].Result.Status)
	}

	err = store.MarkConsumed(results[0].Result.ItemID, "source-a", func(tx *ConsumeTx) error {
		_, err := tx.Enqueue(workitem.Submission{
			Kind:           workitem.KindImplement,
			Source:         "source-a",
			SourceRef:      "TASK-1",
			IdempotencyKey: "source-a/TASK-1/implement",
			Preset:         "linux",
			Payload:        json.RawMessage(`{"task_id":"TASK-1","prompt_context":{"prompt":"implement"}}`),
			MaxAttempts:    3,
		})
		return err
	})
	if err != nil {
		t.Fatalf("MarkConsumed() error = %v", err)
	}

	results, err = store.ListUnconsumedResults("source-a")
	if err != nil {
		t.Fatalf("ListUnconsumedResults(after consume) error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("ListUnconsumedResults(after consume) len = %d, want 0", len(results))
	}

	followUp, err := store.Claim("runner-a", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(follow-up) error = %v", err)
	}
	if followUp == nil {
		t.Fatalf("Claim(follow-up) returned nil")
	}
	if followUp.Kind != workitem.KindImplement {
		t.Fatalf("follow-up kind = %q, want implement", followUp.Kind)
	}
}

func TestBlockedResultDoesNotSatisfyDependencies(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	parent, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         "source-a",
		SourceRef:      "TASK-parent",
		IdempotencyKey: "source-a/TASK-parent/implement",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-parent"}`),
	})
	if err != nil {
		t.Fatalf("Submit(parent) error = %v", err)
	}
	child, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindImplement,
		Source:         "source-a",
		SourceRef:      "TASK-child",
		IdempotencyKey: "source-a/TASK-child/implement",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-child"}`),
	})
	if err != nil {
		t.Fatalf("Submit(child) error = %v", err)
	}
	insertWorkQueueDependency(t, store.db, child.ID, parent.ID)

	if err := store.Block(parent.ID, Result{
		Payload: json.RawMessage(`{"status":"blocked","reason":"runner timeout"}`),
	}); err != nil {
		t.Fatalf("Block(parent) error = %v", err)
	}

	assertWorkQueueState(t, store.db, parent.ID, "blocked")
	results, err := store.ListUnconsumedResults("source-a")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	if results[0].Item.ID != parent.ID || results[0].Item.State != "blocked" {
		t.Fatalf("blocked result item = %#v, want parent in blocked state", results[0].Item)
	}
	if results[0].Result.Status != ResultStatusBlocked {
		t.Fatalf("blocked result status = %q, want blocked", results[0].Result.Status)
	}

	claimed, err := store.Claim("runner-a", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(child blocked by parent) error = %v", err)
	}
	if claimed != nil {
		t.Fatalf("Claim(child blocked by parent) = %q, want nil", claimed.ID)
	}
	assertWorkQueueState(t, store.db, child.ID, "pending")
}

func TestRequeueStaleRequeuesThenFailsWithSynthesizedResult(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	base := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	insertClaimedWorkQueueItem(t, store, staleItem{
		id:             "stale-retry",
		attempt:        1,
		maxAttempts:    2,
		claimedBy:      "dead-runner",
		heartbeatAt:    base.Add(-10 * time.Minute),
		leaseExpiresAt: base.Add(-5 * time.Minute),
	})

	requeued, err := store.RequeueStale(base)
	if err != nil {
		t.Fatalf("RequeueStale(retry) error = %v", err)
	}
	if requeued != 1 {
		t.Fatalf("RequeueStale(retry) = %d, want 1", requeued)
	}
	assertRequeuedPending(t, store, "stale-retry", base.Add(2*time.Minute))
	assertNoWorkQueueResult(t, store, "stale-retry")

	claimedAgain, err := store.Claim("runner-b", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(before not_before) error = %v", err)
	}
	if claimedAgain != nil {
		t.Fatalf("Claim(before not_before) = %q, want nil", claimedAgain.ID)
	}

	forceClaimedWorkQueueAttempt(t, store, "stale-retry", 2, "dead-runner-2", base.Add(3*time.Minute), base.Add(4*time.Minute))
	requeued, err = store.RequeueStale(base.Add(5 * time.Minute))
	if err != nil {
		t.Fatalf("RequeueStale(fail) error = %v", err)
	}
	if requeued != 1 {
		t.Fatalf("RequeueStale(fail) = %d, want 1", requeued)
	}

	assertWorkQueueState(t, store.db, "stale-retry", "failed")
	result := readWorkQueueResult(t, store, "stale-retry")
	if result.Status != "failed" {
		t.Fatalf("result.Status = %q, want failed", result.Status)
	}
	if !strings.Contains(string(result.Payload), "stale lease expired") {
		t.Fatalf("result.Payload = %s, want synthesized stale lease reason", result.Payload)
	}
	if result.StartedAt.IsZero() || result.FinishedAt.IsZero() {
		t.Fatalf("result timestamps were not set: %#v", result)
	}
}

func TestRecoverRetryableFailuresForSourceRefRequeuesAndClearsResult(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	item, err := store.Submit(Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcpr",
		SourceRef:      "pr:14330209",
		IdempotencyKey: "arcpr/14330209/review",
		Preset:         "arcpr",
		Payload:        json.RawMessage(`{"pr_id":"14330209"}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	claimed, err := store.Claim("runner-a", []string{"arcpr"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil || claimed.ID != item.ID {
		t.Fatalf("Claim() = %#v, want %q", claimed, item.ID)
	}
	if err := store.Fail(item.ID, Result{Payload: json.RawMessage(`{"reason":"temporary mount failure"}`)}); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	recovered, err := store.RecoverRetryableFailuresForSourceRef("pr:14330209")
	if err != nil {
		t.Fatalf("RecoverRetryableFailuresForSourceRef() error = %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}
	assertWorkQueueState(t, store.db, item.ID, "pending")
	results, err := store.ListUnconsumedResults("arcpr")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("unconsumed results = %#v, want none after recovery", results)
	}
	reclaimed, err := store.Claim("runner-b", []string{"arcpr"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(recovered) error = %v", err)
	}
	if reclaimed == nil || reclaimed.ID != item.ID || reclaimed.Attempt != 2 {
		t.Fatalf("Claim(recovered) = %#v, want attempt 2 for %q", reclaimed, item.ID)
	}
}

type staleItem struct {
	id             string
	attempt        int
	maxAttempts    int
	claimedBy      string
	heartbeatAt    time.Time
	leaseExpiresAt time.Time
}

func insertClaimedWorkQueueItem(t *testing.T, store *Store, item staleItem) {
	t.Helper()

	createdAt := item.heartbeatAt.Add(-time.Minute)
	_, err := store.db.Exec(`
INSERT INTO work_items (
	id, kind, source, source_ref, idempotency_key, preset, priority, payload,
	state, attempt, max_attempts, not_before, claimed_by, lease_expires_at,
	heartbeat_at, created_at, updated_at
) VALUES (?, 'implement', 'test-source', ?, ?, 'linux', 0, '{}',
	'claimed', ?, ?, '', ?, ?, ?, ?, ?)`,
		item.id,
		item.id,
		"test-key/"+item.id,
		item.attempt,
		item.maxAttempts,
		item.claimedBy,
		formatTestQueueTime(item.leaseExpiresAt),
		formatTestQueueTime(item.heartbeatAt),
		formatTestQueueTime(createdAt),
		formatTestQueueTime(item.heartbeatAt),
	)
	if err != nil {
		t.Fatalf("insert claimed item %q: %v", item.id, err)
	}
}

func assertRequeuedPending(t *testing.T, store *Store, itemID string, wantNotBefore time.Time) {
	t.Helper()

	var state string
	var claimedBy string
	var leaseExpiresAt string
	var heartbeatAt string
	var notBefore string
	if err := store.db.QueryRow(`
SELECT state, claimed_by, lease_expires_at, heartbeat_at, not_before
FROM work_items
WHERE id = ?`, itemID).Scan(&state, &claimedBy, &leaseExpiresAt, &heartbeatAt, &notBefore); err != nil {
		t.Fatalf("read requeued item %q: %v", itemID, err)
	}
	if state != "pending" {
		t.Fatalf("state = %q, want pending", state)
	}
	if claimedBy != "" || leaseExpiresAt != "" || heartbeatAt != "" {
		t.Fatalf("lease fields were not cleared: claimed_by=%q lease=%q heartbeat=%q", claimedBy, leaseExpiresAt, heartbeatAt)
	}
	if got := parseTestQueueTime(t, notBefore); !got.Equal(wantNotBefore) {
		t.Fatalf("not_before = %v, want %v", got, wantNotBefore)
	}
}

func assertNoWorkQueueResult(t *testing.T, store *Store, itemID string) {
	t.Helper()

	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM work_results WHERE item_id = ?", itemID).Scan(&count); err != nil {
		t.Fatalf("count result rows for %q: %v", itemID, err)
	}
	if count != 0 {
		t.Fatalf("work_results rows for %q = %d, want 0", itemID, count)
	}
}

func forceClaimedWorkQueueAttempt(t *testing.T, store *Store, itemID string, attempt int, claimedBy string, heartbeatAt time.Time, leaseExpiresAt time.Time) {
	t.Helper()

	result, err := store.db.Exec(`
UPDATE work_items
SET state = 'claimed',
	attempt = ?,
	claimed_by = ?,
	heartbeat_at = ?,
	lease_expires_at = ?,
	not_before = '',
	updated_at = ?
WHERE id = ?`,
		attempt,
		claimedBy,
		formatTestQueueTime(heartbeatAt),
		formatTestQueueTime(leaseExpiresAt),
		formatTestQueueTime(heartbeatAt),
		itemID,
	)
	if err != nil {
		t.Fatalf("force claimed attempt for %q: %v", itemID, err)
	}
	assertRowsAffected(t, result, "force claimed attempt")
}

func readWorkQueueResult(t *testing.T, store *Store, itemID string) Result {
	t.Helper()

	var result Result
	var payload string
	var startedAt string
	var finishedAt string
	var consumedAt string
	if err := store.db.QueryRow(`
SELECT item_id, status, payload, log_path, started_at, finished_at, consumed_at, consumed_by
FROM work_results
WHERE item_id = ?`, itemID).Scan(
		&result.ItemID,
		&result.Status,
		&payload,
		&result.LogPath,
		&startedAt,
		&finishedAt,
		&consumedAt,
		&result.ConsumedBy,
	); err != nil {
		t.Fatalf("read result for %q: %v", itemID, err)
	}
	result.Payload = json.RawMessage(payload)
	result.StartedAt = parseTestQueueTime(t, startedAt)
	result.FinishedAt = parseTestQueueTime(t, finishedAt)
	if consumedAt != "" {
		result.ConsumedAt = parseTestQueueTime(t, consumedAt)
	}

	if !json.Valid([]byte(result.Payload)) {
		t.Fatalf("result payload is not JSON: %s", result.Payload)
	}
	return result
}
