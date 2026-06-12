package workqueue

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

type Item = workitem.Item
type Submission = workitem.Submission

const (
	defaultMaxAttempts = 3
	itemStatePending   = "pending"
)

func (s *Store) Enqueue(submission Submission) (Item, error) {
	return s.EnqueueWithDeps(submission, nil)
}

func (s *Store) EnqueueWithDeps(submission Submission, deps []string) (Item, error) {
	if s == nil || s.db == nil {
		return Item{}, fmt.Errorf("workqueue store is not open")
	}

	normalized, err := normalizeSubmission(submission)
	if err != nil {
		return Item{}, err
	}
	normalizedDeps, err := normalizeDependencyIDs(deps)
	if err != nil {
		return Item{}, err
	}
	if err := s.enableForeignKeys(); err != nil {
		return Item{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Item{}, fmt.Errorf("begin enqueue transaction: %w", err)
	}
	defer tx.Rollback()

	item, inserted, err := enqueueItem(tx, normalized)
	if err != nil {
		return Item{}, err
	}
	if inserted {
		for _, depID := range normalizedDeps {
			if _, err := tx.Exec(`
INSERT INTO item_deps (item_id, depends_on)
VALUES (?, ?)`,
				item.ID,
				depID,
			); err != nil {
				return Item{}, fmt.Errorf("enqueue dependency %q for item %q: %w", depID, item.ID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit enqueue transaction: %w", err)
	}
	return item, nil
}

func (s *Store) enableForeignKeys() error {
	if _, err := s.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable workqueue foreign keys: %w", err)
	}
	return nil
}

func enqueueItem(tx *sql.Tx, submission Submission) (Item, bool, error) {
	now := time.Now().UTC()
	itemID, err := newWorkItemID(now)
	if err != nil {
		return Item{}, false, err
	}

	result, err := tx.Exec(`
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(idempotency_key) DO NOTHING`,
		itemID,
		string(submission.Kind),
		submission.Source,
		submission.SourceRef,
		submission.IdempotencyKey,
		submission.Preset,
		submission.Priority,
		string(submission.Payload),
		itemStatePending,
		0,
		submission.MaxAttempts,
		"",
		"",
		"",
		"",
		formatQueueTime(now),
		formatQueueTime(now),
	)
	if err != nil {
		return Item{}, false, fmt.Errorf("enqueue work item %q: %w", submission.IdempotencyKey, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Item{}, false, fmt.Errorf("enqueue work item %q rows affected: %w", submission.IdempotencyKey, err)
	}

	item, err := getItemByIdempotencyKeyTx(tx, submission.IdempotencyKey)
	if err != nil {
		return Item{}, false, err
	}
	return item, rowsAffected > 0, nil
}

func normalizeSubmission(submission Submission) (Submission, error) {
	if err := submission.Kind.Validate(); err != nil {
		return Submission{}, err
	}
	submission.Source = strings.TrimSpace(submission.Source)
	if submission.Source == "" {
		return Submission{}, fmt.Errorf("source is required")
	}
	submission.SourceRef = strings.TrimSpace(submission.SourceRef)
	if submission.SourceRef == "" {
		return Submission{}, fmt.Errorf("source ref is required")
	}
	submission.IdempotencyKey = strings.TrimSpace(submission.IdempotencyKey)
	if submission.IdempotencyKey == "" {
		return Submission{}, fmt.Errorf("idempotency key is required")
	}
	submission.Preset = strings.TrimSpace(submission.Preset)
	if submission.Preset == "" {
		return Submission{}, fmt.Errorf("preset is required")
	}
	if len(submission.Payload) == 0 {
		submission.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(submission.Payload) {
		return Submission{}, fmt.Errorf("payload must be valid JSON")
	}
	if submission.MaxAttempts <= 0 {
		submission.MaxAttempts = defaultMaxAttempts
	}
	return submission, nil
}

func normalizeDependencyIDs(deps []string) ([]string, error) {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(deps))
	for _, depID := range deps {
		depID = strings.TrimSpace(depID)
		if depID == "" {
			return nil, fmt.Errorf("dependency item ID is required")
		}
		if seen[depID] {
			continue
		}
		seen[depID] = true
		normalized = append(normalized, depID)
	}
	return normalized, nil
}

func getItemByIdempotencyKeyTx(tx *sql.Tx, idempotencyKey string) (Item, error) {
	item, err := scanItem(tx.QueryRow(itemSelectSQL()+" WHERE idempotency_key = ?", idempotencyKey))
	if err != nil {
		if err == sql.ErrNoRows {
			return Item{}, err
		}
		return Item{}, fmt.Errorf("get work item by idempotency key %q: %w", idempotencyKey, err)
	}
	return item, nil
}

type itemScanner interface {
	Scan(dest ...any) error
}

func scanItem(scanner itemScanner) (Item, error) {
	var item Item
	var kind string
	var payload string
	var notBefore string
	var leaseExpiresAt string
	var heartbeatAt string
	var createdAt string
	var updatedAt string

	if err := scanner.Scan(
		&item.ID,
		&kind,
		&item.Source,
		&item.SourceRef,
		&item.IdempotencyKey,
		&item.Preset,
		&item.Priority,
		&payload,
		&item.State,
		&item.Attempt,
		&item.MaxAttempts,
		&notBefore,
		&item.ClaimedBy,
		&leaseExpiresAt,
		&heartbeatAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Item{}, err
	}

	var err error
	item.Kind = workitem.Kind(kind)
	item.Payload = []byte(payload)
	if item.NotBefore, err = parseQueueTime("not_before", notBefore); err != nil {
		return Item{}, err
	}
	if item.LeaseExpiresAt, err = parseQueueTime("lease_expires_at", leaseExpiresAt); err != nil {
		return Item{}, err
	}
	if item.HeartbeatAt, err = parseQueueTime("heartbeat_at", heartbeatAt); err != nil {
		return Item{}, err
	}
	if item.CreatedAt, err = parseQueueTime("created_at", createdAt); err != nil {
		return Item{}, err
	}
	if item.UpdatedAt, err = parseQueueTime("updated_at", updatedAt); err != nil {
		return Item{}, err
	}
	return item, nil
}

func itemSelectSQL() string {
	return `SELECT
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
FROM work_items`
}

func formatQueueTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseQueueTime(column string, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse work_items.%s %q: %w", column, value, err)
	}
	return parsed, nil
}

func newWorkItemID(now time.Time) (string, error) {
	var data [16]byte
	millis := uint64(now.UTC().UnixMilli())
	data[0] = byte(millis >> 40)
	data[1] = byte(millis >> 32)
	data[2] = byte(millis >> 24)
	data[3] = byte(millis >> 16)
	data[4] = byte(millis >> 8)
	data[5] = byte(millis)
	if _, err := rand.Read(data[6:]); err != nil {
		return "", fmt.Errorf("generate work item ID: %w", err)
	}
	return encodeCrockfordBase32(data[:], 26), nil
}

func encodeCrockfordBase32(data []byte, width int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	n := new(big.Int).SetBytes(data)
	base := big.NewInt(32)
	mod := new(big.Int)
	encoded := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		if n.Sign() == 0 {
			encoded[i] = alphabet[0]
			continue
		}
		n.QuoRem(n, base, mod)
		encoded[i] = alphabet[mod.Int64()]
	}
	return string(encoded)
}
