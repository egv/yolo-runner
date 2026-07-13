package workqueue

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

const defaultListItemsLimit = 500

type ListItemsFilter struct {
	Source    string
	SourceRef string
	State     string
	Preset    string
	Kind      string
	ClaimedBy string
	Limit     int
}

type Dep struct {
	ID        string        `json:"id"`
	Kind      workitem.Kind `json:"kind"`
	SourceRef string        `json:"source_ref"`
	State     string        `json:"state"`
}

type ItemDetail struct {
	Item      workitem.Item `json:"item"`
	Blocks    []Dep         `json:"blocks"`
	BlockedBy []Dep         `json:"blocked_by"`
	Result    *Result       `json:"result,omitempty"`
}

func (s *Store) ListItems(filter ListItemsFilter) ([]workitem.Item, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}

	clauses := make([]string, 0, 5)
	args := make([]any, 0, 6)
	addStringFilter := func(column string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		clauses = append(clauses, column+" = ?")
		args = append(args, value)
	}
	addStringFilter("source", filter.Source)
	addStringFilter("source_ref", filter.SourceRef)
	addStringFilter("state", filter.State)
	addStringFilter("preset", filter.Preset)
	addStringFilter("kind", filter.Kind)
	addStringFilter("claimed_by", filter.ClaimedBy)

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListItemsLimit
	}
	args = append(args, limit)

	query := itemSelectSQL()
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY state, priority DESC, created_at LIMIT ?"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list work items: %w", err)
	}
	defer rows.Close()

	items := []workitem.Item{}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan work item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read work items: %w", err)
	}
	return items, nil
}

func (s *Store) GetItem(id string) (ItemDetail, error) {
	if err := ensureOpenStore(s); err != nil {
		return ItemDetail{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ItemDetail{}, fmt.Errorf("item ID is required")
	}

	detail, err := scanItemDetail(s.db.QueryRow(`
SELECT wi.id, wi.kind, wi.source, wi.source_ref, wi.idempotency_key, wi.preset,
	wi.priority, wi.payload, wi.state, wi.attempt, wi.max_attempts,
	wi.not_before, wi.claimed_by, wi.lease_expires_at, wi.heartbeat_at,
	wi.created_at, wi.updated_at,
	wr.item_id, wr.status, wr.payload, wr.log_path, wr.started_at,
	wr.finished_at, wr.consumed_at, wr.consumed_by
FROM work_items wi
LEFT JOIN work_results wr ON wr.item_id = wi.id
WHERE wi.id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return ItemDetail{}, fmt.Errorf("get work item %q: %w", id, err)
		}
		return ItemDetail{}, fmt.Errorf("get work item %q: %w", id, err)
	}

	detail.Blocks, err = s.listDeps(`
SELECT wi.id, wi.kind, wi.source_ref, wi.state
FROM item_deps dep
JOIN work_items wi ON wi.id = dep.depends_on
WHERE dep.item_id = ?
ORDER BY wi.id`, id)
	if err != nil {
		return ItemDetail{}, fmt.Errorf("list blocked items for %q: %w", id, err)
	}

	detail.BlockedBy, err = s.listDeps(`
SELECT wi.id, wi.kind, wi.source_ref, wi.state
FROM item_deps dep
JOIN work_items wi ON wi.id = dep.item_id
WHERE dep.depends_on = ?
ORDER BY wi.id`, id)
	if err != nil {
		return ItemDetail{}, fmt.Errorf("list blocking items for %q: %w", id, err)
	}

	return detail, nil
}

func (s *Store) listDeps(query string, id string) ([]Dep, error) {
	rows, err := s.db.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deps := []Dep{}
	for rows.Next() {
		var dep Dep
		var kind string
		if err := rows.Scan(&dep.ID, &kind, &dep.SourceRef, &dep.State); err != nil {
			return nil, err
		}
		dep.Kind = workitem.Kind(kind)
		deps = append(deps, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}

func scanItemDetail(scanner itemScanner) (ItemDetail, error) {
	var item workitem.Item
	var kind string
	var payload string
	var notBefore string
	var leaseExpiresAt string
	var heartbeatAt string
	var createdAt string
	var updatedAt string
	var resultItemID sql.NullString
	var resultStatus sql.NullString
	var resultPayload sql.NullString
	var resultLogPath sql.NullString
	var resultStartedAt sql.NullString
	var resultFinishedAt sql.NullString
	var resultConsumedAt sql.NullString
	var resultConsumedBy sql.NullString

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
		&resultItemID,
		&resultStatus,
		&resultPayload,
		&resultLogPath,
		&resultStartedAt,
		&resultFinishedAt,
		&resultConsumedAt,
		&resultConsumedBy,
	); err != nil {
		return ItemDetail{}, err
	}

	var err error
	item.Kind = workitem.Kind(kind)
	item.Payload = json.RawMessage(payload)
	if item.NotBefore, err = parseQueueTime("not_before", notBefore); err != nil {
		return ItemDetail{}, err
	}
	if item.LeaseExpiresAt, err = parseQueueTime("lease_expires_at", leaseExpiresAt); err != nil {
		return ItemDetail{}, err
	}
	if item.HeartbeatAt, err = parseQueueTime("heartbeat_at", heartbeatAt); err != nil {
		return ItemDetail{}, err
	}
	if item.CreatedAt, err = parseQueueTime("created_at", createdAt); err != nil {
		return ItemDetail{}, err
	}
	if item.UpdatedAt, err = parseQueueTime("updated_at", updatedAt); err != nil {
		return ItemDetail{}, err
	}

	detail := ItemDetail{Item: item, Blocks: []Dep{}, BlockedBy: []Dep{}}
	if resultItemID.Valid {
		result := Result{
			ItemID:     resultItemID.String,
			Status:     ResultStatus(resultStatus.String),
			Payload:    json.RawMessage(resultPayload.String),
			LogPath:    resultLogPath.String,
			ConsumedBy: resultConsumedBy.String,
		}
		if result.StartedAt, err = parseQueueTime("started_at", resultStartedAt.String); err != nil {
			return ItemDetail{}, err
		}
		if result.FinishedAt, err = parseQueueTime("finished_at", resultFinishedAt.String); err != nil {
			return ItemDetail{}, err
		}
		if result.ConsumedAt, err = parseQueueTime("consumed_at", resultConsumedAt.String); err != nil {
			return ItemDetail{}, err
		}
		detail.Result = &result
	}
	return detail, nil
}
