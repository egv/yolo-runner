package workqueue

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

const (
	ResultStatusCompleted ResultStatus = "completed"
	ResultStatusBlocked   ResultStatus = "blocked"
	ResultStatusFailed    ResultStatus = "failed"

	itemStateBlocked   = "blocked"
	itemStateRunning   = "running"
	itemStateFailed    = "failed"
	itemStateCancelled = "cancelled"
)

type ResultStatus string

type Result struct {
	ItemID     string          `json:"item_id"`
	Status     ResultStatus    `json:"status"`
	Payload    json.RawMessage `json:"payload"`
	LogPath    string          `json:"log_path"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	ConsumedAt time.Time       `json:"consumed_at"`
	ConsumedBy string          `json:"consumed_by"`
}

type UnconsumedResult struct {
	Item   workitem.Item `json:"item"`
	Result Result        `json:"result"`
}

type MarkConsumedCallback func(*ConsumeTx) error

type ConsumeTx struct {
	tx  *sql.Tx
	now time.Time
}

// Complete writes a completed work result and moves the item to done.
func (s *Store) Complete(itemID string, result Result) error {
	result.Status = ResultStatusCompleted
	return s.finishItem(itemID, itemStateDone, result)
}

// Block writes a blocked work result and moves the item to blocked for source
// consumption. Blocked items are terminal, but they must not satisfy
// dependencies for downstream work.
func (s *Store) Block(itemID string, result Result) error {
	result.Status = ResultStatusBlocked
	return s.finishItem(itemID, itemStateBlocked, result)
}

// Fail writes a failed work result and moves the item to failed.
func (s *Store) Fail(itemID string, result Result) error {
	result.Status = ResultStatusFailed
	return s.finishItem(itemID, itemStateFailed, result)
}

func (s *Store) finishItem(itemID string, terminalState string, result Result) error {
	if err := ensureOpenStore(s); err != nil {
		return err
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("item ID is required")
	}

	now := time.Now().UTC()
	result.ItemID = itemID
	normalized, err := normalizeResult(result, now)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin finish item %q: %w", itemID, err)
	}
	defer tx.Rollback()

	if err := writeTerminalResultTx(tx, itemID, terminalState, normalized, now, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finish item %q: %w", itemID, err)
	}
	return nil
}

// RequeueStale reaps expired leases. Items with attempts remaining are moved
// back to pending with exponential not_before backoff; exhausted items fail
// with a synthesized result row so sources can consume the failure.
func (s *Store) RequeueStale(now time.Time) (int, error) {
	if err := ensureOpenStore(s); err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	formattedNow := formatQueueTime(now)

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin requeue stale leases: %w", err)
	}
	defer tx.Rollback()

	staleItems, err := listStaleLeasesTx(tx, formattedNow)
	if err != nil {
		return 0, err
	}

	reaped := 0
	for _, stale := range staleItems {
		maxAttempts := stale.maxAttempts
		if maxAttempts <= 0 {
			maxAttempts = defaultMaxAttempts
		}

		if stale.attempt < maxAttempts {
			notBefore := now.Add(requeueBackoff(stale.attempt))
			affected, err := execRowsAffected(tx, `
UPDATE work_items
SET state = ?,
	not_before = ?,
	claimed_by = '',
	lease_expires_at = '',
	heartbeat_at = '',
	updated_at = ?
WHERE id = ?
	AND state IN (?, ?)
	AND lease_expires_at = ?`,
				itemStatePending,
				formatQueueTime(notBefore),
				formattedNow,
				stale.id,
				itemStateClaimed,
				itemStateRunning,
				stale.leaseExpiresAt,
			)
			if err != nil {
				return 0, fmt.Errorf("requeue stale item %q: %w", stale.id, err)
			}
			if affected == 1 {
				reaped++
			}
			continue
		}

		result, err := synthesizedStaleFailure(stale, now)
		if err != nil {
			return 0, err
		}
		affected, err := writeFailedStaleResultTx(tx, stale.id, stale.leaseExpiresAt, result, now)
		if err != nil {
			return 0, err
		}
		if affected == 1 {
			reaped++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit requeue stale leases: %w", err)
	}
	return reaped, nil
}

// ListUnconsumedResults returns terminal results for a source that have not
// been marked consumed yet, joined with their opaque work item.
func (s *Store) ListUnconsumedResults(source string) ([]UnconsumedResult, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("source is required")
	}

	rows, err := s.db.Query(`
SELECT wi.id, wi.kind, wi.source, wi.source_ref, wi.idempotency_key, wi.preset,
	wi.priority, wi.payload, wi.state, wi.attempt, wi.max_attempts,
	wi.not_before, wi.claimed_by, wi.lease_expires_at, wi.heartbeat_at,
	wi.created_at, wi.updated_at,
	wr.item_id, wr.status, wr.payload, wr.log_path, wr.started_at,
	wr.finished_at, wr.consumed_at, wr.consumed_by
FROM work_results wr
JOIN work_items wi ON wi.id = wr.item_id
WHERE wi.source = ? AND wr.consumed_at = ''
ORDER BY wr.finished_at ASC, wi.id ASC`, source)
	if err != nil {
		return nil, fmt.Errorf("list unconsumed results for source %q: %w", source, err)
	}
	defer rows.Close()

	var results []UnconsumedResult
	for rows.Next() {
		result, err := scanUnconsumedResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read unconsumed results for source %q: %w", source, err)
	}
	return results, nil
}

// MarkConsumed marks a result consumed and runs callback(s) in the same
// transaction. Callbacks can enqueue follow-up items through ConsumeTx.Enqueue.
func (s *Store) MarkConsumed(itemID string, consumedBy string, callbacks ...MarkConsumedCallback) error {
	if err := ensureOpenStore(s); err != nil {
		return err
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("item ID is required")
	}
	consumedBy = strings.TrimSpace(consumedBy)
	if consumedBy == "" {
		return fmt.Errorf("consumer is required")
	}

	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mark result %q consumed: %w", itemID, err)
	}
	defer tx.Rollback()

	affected, err := execRowsAffected(tx, `
UPDATE work_results
SET consumed_at = ?, consumed_by = ?
WHERE item_id = ? AND consumed_at = ''`,
		formatQueueTime(now),
		consumedBy,
		itemID,
	)
	if err != nil {
		return fmt.Errorf("mark result %q consumed: %w", itemID, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}

	consumeTx := &ConsumeTx{tx: tx, now: now}
	for _, callback := range callbacks {
		if callback == nil {
			continue
		}
		if err := callback(consumeTx); err != nil {
			return fmt.Errorf("mark result %q consumed callback: %w", itemID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark result %q consumed: %w", itemID, err)
	}
	return nil
}

func (s *Store) MarkConsumedWithFollowUps(itemID string, consumedBy string, followUps []workitem.Submission) error {
	return s.MarkConsumed(itemID, consumedBy, func(tx *ConsumeTx) error {
		for _, followUp := range followUps {
			if _, err := tx.Enqueue(followUp); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) Submit(submission workitem.Submission) (*workitem.Item, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin submit work item: %w", err)
	}
	defer tx.Rollback()

	item, err := enqueueSubmissionTx(tx, submission, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit submit work item: %w", err)
	}
	return item, nil
}

func (tx *ConsumeTx) Enqueue(submission workitem.Submission) (*workitem.Item, error) {
	if tx == nil || tx.tx == nil {
		return nil, fmt.Errorf("consume transaction is not active")
	}
	return enqueueSubmissionTx(tx.tx, submission, tx.now)
}

type staleLease struct {
	id             string
	attempt        int
	maxAttempts    int
	claimedBy      string
	leaseExpiresAt string
	heartbeatAt    string
}

func listStaleLeasesTx(tx *sql.Tx, now string) ([]staleLease, error) {
	rows, err := tx.Query(`
SELECT id, attempt, max_attempts, claimed_by, lease_expires_at, heartbeat_at
FROM work_items
WHERE state IN (?, ?)
	AND lease_expires_at != ''
	AND lease_expires_at <= ?
ORDER BY lease_expires_at ASC, id ASC`,
		itemStateClaimed,
		itemStateRunning,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("list stale leases: %w", err)
	}
	defer rows.Close()

	var staleItems []staleLease
	for rows.Next() {
		var stale staleLease
		if err := rows.Scan(
			&stale.id,
			&stale.attempt,
			&stale.maxAttempts,
			&stale.claimedBy,
			&stale.leaseExpiresAt,
			&stale.heartbeatAt,
		); err != nil {
			return nil, fmt.Errorf("scan stale lease: %w", err)
		}
		staleItems = append(staleItems, stale)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read stale leases: %w", err)
	}
	return staleItems, nil
}

func requeueBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 30 {
		attempt = 30
	}
	return time.Duration(1<<attempt) * time.Minute
}

func synthesizedStaleFailure(stale staleLease, now time.Time) (Result, error) {
	startedAt, err := parseQueueTime("heartbeat_at", stale.heartbeatAt)
	if err != nil {
		return Result{}, fmt.Errorf("synthesize stale failure for item %q: %w", stale.id, err)
	}
	if startedAt.IsZero() {
		startedAt = now
	}

	payload, err := json.Marshal(map[string]any{
		"status":           string(ResultStatusFailed),
		"reason":           fmt.Sprintf("stale lease expired after attempt %d of %d", stale.attempt, stale.maxAttempts),
		"claimed_by":       stale.claimedBy,
		"lease_expires_at": stale.leaseExpiresAt,
	})
	if err != nil {
		return Result{}, fmt.Errorf("synthesize stale failure for item %q: %w", stale.id, err)
	}

	return Result{
		ItemID:     stale.id,
		Status:     ResultStatusFailed,
		Payload:    payload,
		StartedAt:  startedAt,
		FinishedAt: now,
	}, nil
}

func writeFailedStaleResultTx(tx *sql.Tx, itemID string, expectedLease string, result Result, now time.Time) (int64, error) {
	normalized, err := normalizeResult(result, now)
	if err != nil {
		return 0, err
	}

	affected, err := execRowsAffected(tx, `
UPDATE work_items
SET state = ?,
	claimed_by = '',
	lease_expires_at = '',
	heartbeat_at = '',
	updated_at = ?
WHERE id = ?
	AND state IN (?, ?)
	AND lease_expires_at = ?`,
		itemStateFailed,
		formatQueueTime(now),
		itemID,
		itemStateClaimed,
		itemStateRunning,
		expectedLease,
	)
	if err != nil {
		return 0, fmt.Errorf("fail stale item %q: %w", itemID, err)
	}
	if affected == 0 {
		return 0, nil
	}
	if err := insertResultTx(tx, normalized); err != nil {
		return 0, err
	}
	return affected, nil
}

func writeTerminalResultTx(tx *sql.Tx, itemID string, terminalState string, result Result, now time.Time, expectedLease string) error {
	query := `
UPDATE work_items
SET state = ?,
	claimed_by = '',
	lease_expires_at = '',
	heartbeat_at = '',
	updated_at = ?
WHERE id = ?
	AND state NOT IN (?, ?, ?, ?)`
	args := []any{
		terminalState,
		formatQueueTime(now),
		itemID,
		itemStateDone,
		itemStateBlocked,
		itemStateFailed,
		itemStateCancelled,
	}
	if expectedLease != "" {
		query += " AND lease_expires_at = ?"
		args = append(args, expectedLease)
	}

	affected, err := execRowsAffected(tx, query, args...)
	if err != nil {
		return fmt.Errorf("finish item %q: %w", itemID, err)
	}
	if affected == 0 {
		return terminalUpdateErr(tx, itemID)
	}
	if err := insertResultTx(tx, result); err != nil {
		return err
	}
	return nil
}

func insertResultTx(tx *sql.Tx, result Result) error {
	_, err := tx.Exec(`
INSERT INTO work_results (
	item_id, status, payload, log_path, started_at, finished_at,
	consumed_at, consumed_by
) VALUES (?, ?, ?, ?, ?, ?, '', '')`,
		result.ItemID,
		string(result.Status),
		string(result.Payload),
		result.LogPath,
		formatQueueTime(result.StartedAt),
		formatQueueTime(result.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert result for item %q: %w", result.ItemID, err)
	}
	return nil
}

func terminalUpdateErr(tx *sql.Tx, itemID string) error {
	var state string
	err := tx.QueryRow("SELECT state FROM work_items WHERE id = ?", itemID).Scan(&state)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("read item %q state: %w", itemID, err)
	}
	return fmt.Errorf("item %q is already terminal with state %q", itemID, state)
}

func normalizeResult(result Result, now time.Time) (Result, error) {
	if strings.TrimSpace(result.ItemID) == "" {
		return Result{}, fmt.Errorf("result item ID is required")
	}
	if err := validateResultStatus(result.Status); err != nil {
		return Result{}, err
	}

	payload, err := normalizeJSON(result.Payload, "result payload")
	if err != nil {
		return Result{}, err
	}
	result.Payload = payload
	if result.StartedAt.IsZero() {
		result.StartedAt = now
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = now
	}
	result.StartedAt = result.StartedAt.UTC()
	result.FinishedAt = result.FinishedAt.UTC()
	return result, nil
}

func validateResultStatus(status ResultStatus) error {
	switch status {
	case ResultStatusCompleted, ResultStatusBlocked, ResultStatusFailed:
		return nil
	default:
		return fmt.Errorf("invalid result status %q", status)
	}
}

func normalizeJSON(payload json.RawMessage, field string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("%s must be valid JSON", field)
	}
	return json.RawMessage(trimmed), nil
}

func enqueueSubmissionTx(tx *sql.Tx, submission workitem.Submission, now time.Time) (*workitem.Item, error) {
	if err := validateSubmission(submission); err != nil {
		return nil, err
	}
	payload, err := normalizeJSON(submission.Payload, "submission payload")
	if err != nil {
		return nil, err
	}

	maxAttempts := submission.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	id, err := newQueueItemID(now)
	if err != nil {
		return nil, err
	}

	affected, err := execRowsAffected(tx, `
INSERT INTO work_items (
	id, kind, source, source_ref, idempotency_key, preset, priority, payload,
	state, attempt, max_attempts, not_before, claimed_by, lease_expires_at,
	heartbeat_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, '', '', '', '', ?, ?)
ON CONFLICT(idempotency_key) DO NOTHING`,
		id,
		string(submission.Kind),
		strings.TrimSpace(submission.Source),
		strings.TrimSpace(submission.SourceRef),
		strings.TrimSpace(submission.IdempotencyKey),
		strings.TrimSpace(submission.Preset),
		submission.Priority,
		string(payload),
		itemStatePending,
		maxAttempts,
		formatQueueTime(now),
		formatQueueTime(now),
	)
	if err != nil {
		return nil, fmt.Errorf("enqueue work item %q: %w", submission.IdempotencyKey, err)
	}
	if affected == 0 {
		return selectQueueItemByIdempotencyKeyTx(tx, strings.TrimSpace(submission.IdempotencyKey))
	}
	return selectQueueItemByIDTx(tx, id)
}

func validateSubmission(submission workitem.Submission) error {
	if err := submission.Kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(submission.Source) == "" {
		return fmt.Errorf("submission source is required")
	}
	if strings.TrimSpace(submission.SourceRef) == "" {
		return fmt.Errorf("submission source ref is required")
	}
	if strings.TrimSpace(submission.IdempotencyKey) == "" {
		return fmt.Errorf("submission idempotency key is required")
	}
	if strings.TrimSpace(submission.Preset) == "" {
		return fmt.Errorf("submission preset is required")
	}
	return nil
}

func selectQueueItemByIDTx(tx *sql.Tx, id string) (*workitem.Item, error) {
	item, err := scanQueueItem(tx.QueryRow(`
SELECT id, kind, source, source_ref, idempotency_key, preset, priority,
	payload, state, attempt, max_attempts, not_before, claimed_by,
	lease_expires_at, heartbeat_at, created_at, updated_at
FROM work_items
WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("read work item %q: %w", id, err)
	}
	return item, nil
}

func selectQueueItemByIdempotencyKeyTx(tx *sql.Tx, key string) (*workitem.Item, error) {
	item, err := scanQueueItem(tx.QueryRow(`
SELECT id, kind, source, source_ref, idempotency_key, preset, priority,
	payload, state, attempt, max_attempts, not_before, claimed_by,
	lease_expires_at, heartbeat_at, created_at, updated_at
FROM work_items
WHERE idempotency_key = ?`, key))
	if err != nil {
		return nil, fmt.Errorf("read work item for idempotency key %q: %w", key, err)
	}
	return item, nil
}

func newQueueItemID(now time.Time) (string, error) {
	var random [10]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate work item ID: %w", err)
	}
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(random[:])), nil
}

func scanUnconsumedResult(scanner interface{ Scan(dest ...any) error }) (UnconsumedResult, error) {
	var item workitem.Item
	var kind string
	var itemPayload string
	var notBefore string
	var leaseExpiresAt string
	var heartbeatAt string
	var itemCreatedAt string
	var itemUpdatedAt string
	var result Result
	var resultPayload string
	var startedAt string
	var finishedAt string
	var consumedAt string

	if err := scanner.Scan(
		&item.ID,
		&kind,
		&item.Source,
		&item.SourceRef,
		&item.IdempotencyKey,
		&item.Preset,
		&item.Priority,
		&itemPayload,
		&item.State,
		&item.Attempt,
		&item.MaxAttempts,
		&notBefore,
		&item.ClaimedBy,
		&leaseExpiresAt,
		&heartbeatAt,
		&itemCreatedAt,
		&itemUpdatedAt,
		&result.ItemID,
		&result.Status,
		&resultPayload,
		&result.LogPath,
		&startedAt,
		&finishedAt,
		&consumedAt,
		&result.ConsumedBy,
	); err != nil {
		return UnconsumedResult{}, fmt.Errorf("scan unconsumed result: %w", err)
	}

	var err error
	item.Kind = workitem.Kind(kind)
	item.Payload = json.RawMessage(itemPayload)
	if item.NotBefore, err = parseQueueTime("not_before", notBefore); err != nil {
		return UnconsumedResult{}, err
	}
	if item.LeaseExpiresAt, err = parseQueueTime("lease_expires_at", leaseExpiresAt); err != nil {
		return UnconsumedResult{}, err
	}
	if item.HeartbeatAt, err = parseQueueTime("heartbeat_at", heartbeatAt); err != nil {
		return UnconsumedResult{}, err
	}
	if item.CreatedAt, err = parseQueueTime("created_at", itemCreatedAt); err != nil {
		return UnconsumedResult{}, err
	}
	if item.UpdatedAt, err = parseQueueTime("updated_at", itemUpdatedAt); err != nil {
		return UnconsumedResult{}, err
	}

	result.Payload = json.RawMessage(resultPayload)
	if result.StartedAt, err = parseQueueTime("started_at", startedAt); err != nil {
		return UnconsumedResult{}, err
	}
	if result.FinishedAt, err = parseQueueTime("finished_at", finishedAt); err != nil {
		return UnconsumedResult{}, err
	}
	if result.ConsumedAt, err = parseQueueTime("consumed_at", consumedAt); err != nil {
		return UnconsumedResult{}, err
	}
	return UnconsumedResult{Item: item, Result: result}, nil
}

func execRowsAffected(execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}, query string, args ...any) (int64, error) {
	result, err := execer.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func ensureOpenStore(s *Store) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("workqueue store is not open")
	}
	return nil
}
