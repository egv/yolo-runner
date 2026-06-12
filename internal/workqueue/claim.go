package workqueue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

const (
	itemStatePending = "pending"
	itemStateClaimed = "claimed"
	itemStateDone    = "done"
)

// Claim atomically leases the oldest highest-priority pending item matching one
// of the runner's presets whose dependencies are all done and whose not_before
// timestamp has passed.
func (s *Store) Claim(runnerID string, presets []string, leaseTTL time.Duration) (*workitem.Item, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("workqueue store is not open")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil, fmt.Errorf("runner ID is required")
	}
	if leaseTTL <= 0 {
		return nil, fmt.Errorf("lease TTL must be positive")
	}

	presets = normalizeClaimPresets(presets)
	if len(presets) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()
	formattedNow := formatQueueTime(now)
	leaseExpiresAt := formatQueueTime(now.Add(leaseTTL))
	presetPlaceholders := strings.TrimRight(strings.Repeat("?,", len(presets)), ",")

	query := fmt.Sprintf(`
UPDATE work_items
SET state = ?,
	claimed_by = ?,
	attempt = attempt + 1,
	heartbeat_at = ?,
	lease_expires_at = ?,
	updated_at = ?
WHERE id = (
	SELECT wi.id
	FROM work_items wi
	WHERE wi.state = ?
		AND wi.preset IN (%s)
		AND (wi.not_before = '' OR wi.not_before <= ?)
		AND NOT EXISTS (
			SELECT 1
			FROM item_deps dep
			LEFT JOIN work_items dependency ON dependency.id = dep.depends_on
			WHERE dep.item_id = wi.id
				AND COALESCE(dependency.state, '') != ?
		)
	ORDER BY wi.priority DESC, wi.created_at ASC, wi.id ASC
	LIMIT 1
)
RETURNING id, kind, source, source_ref, idempotency_key, preset, priority,
	payload, state, attempt, max_attempts, not_before, claimed_by,
	lease_expires_at, heartbeat_at, created_at, updated_at`, presetPlaceholders)

	args := []any{
		itemStateClaimed,
		runnerID,
		formattedNow,
		leaseExpiresAt,
		formattedNow,
		itemStatePending,
	}
	for _, preset := range presets {
		args = append(args, preset)
	}
	args = append(args, formattedNow, itemStateDone)

	item, err := scanQueueItem(s.db.QueryRow(query, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim work item for runner %q: %w", runnerID, err)
	}
	return item, nil
}

// Heartbeat records liveness for a claimed item and extends the existing lease
// by the same interval used for the previous lease.
func (s *Store) Heartbeat(itemID string, runnerID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("workqueue store is not open")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("item ID is required")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return fmt.Errorf("runner ID is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin heartbeat for item %q: %w", itemID, err)
	}
	defer tx.Rollback()

	var heartbeatAt string
	var leaseExpiresAt string
	err = tx.QueryRow(`
SELECT heartbeat_at, lease_expires_at
FROM work_items
WHERE id = ? AND state = ? AND claimed_by = ?`,
		itemID,
		itemStateClaimed,
		runnerID,
	).Scan(&heartbeatAt, &leaseExpiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return err
		}
		return fmt.Errorf("read lease for item %q: %w", itemID, err)
	}

	previousHeartbeat, err := parseQueueTime("heartbeat_at", heartbeatAt)
	if err != nil {
		return fmt.Errorf("read lease for item %q: %w", itemID, err)
	}
	previousLeaseExpiresAt, err := parseQueueTime("lease_expires_at", leaseExpiresAt)
	if err != nil {
		return fmt.Errorf("read lease for item %q: %w", itemID, err)
	}
	if previousHeartbeat.IsZero() || previousLeaseExpiresAt.IsZero() || !previousLeaseExpiresAt.After(previousHeartbeat) {
		return fmt.Errorf("item %q has invalid lease timestamps", itemID)
	}

	now := time.Now().UTC()
	leaseTTL := previousLeaseExpiresAt.Sub(previousHeartbeat)
	result, err := tx.Exec(`
UPDATE work_items
SET heartbeat_at = ?,
	lease_expires_at = ?,
	updated_at = ?
WHERE id = ? AND state = ? AND claimed_by = ?`,
		formatQueueTime(now),
		formatQueueTime(now.Add(leaseTTL)),
		formatQueueTime(now),
		itemID,
		itemStateClaimed,
		runnerID,
	)
	if err != nil {
		return fmt.Errorf("heartbeat item %q: %w", itemID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("heartbeat item %q rows affected: %w", itemID, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit heartbeat for item %q: %w", itemID, err)
	}
	return nil
}

type queueItemScanner interface {
	Scan(dest ...any) error
}

func scanQueueItem(scanner queueItemScanner) (*workitem.Item, error) {
	var item workitem.Item
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
		return nil, err
	}

	var err error
	item.Kind = workitem.Kind(kind)
	item.Payload = json.RawMessage(payload)
	if item.NotBefore, err = parseQueueTime("not_before", notBefore); err != nil {
		return nil, err
	}
	if item.LeaseExpiresAt, err = parseQueueTime("lease_expires_at", leaseExpiresAt); err != nil {
		return nil, err
	}
	if item.HeartbeatAt, err = parseQueueTime("heartbeat_at", heartbeatAt); err != nil {
		return nil, err
	}
	if item.CreatedAt, err = parseQueueTime("created_at", createdAt); err != nil {
		return nil, err
	}
	if item.UpdatedAt, err = parseQueueTime("updated_at", updatedAt); err != nil {
		return nil, err
	}
	return &item, nil
}

func normalizeClaimPresets(presets []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(presets))
	for _, preset := range presets {
		preset = strings.TrimSpace(preset)
		if preset == "" || seen[preset] {
			continue
		}
		seen[preset] = true
		normalized = append(normalized, preset)
	}
	return normalized
}

func formatQueueTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseQueueTime(field string, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}
