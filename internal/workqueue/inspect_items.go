package workqueue

import (
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

const defaultListItemsLimit = 500

type ListItemsFilter struct {
	Source    string
	State     string
	Preset    string
	Kind      string
	ClaimedBy string
	Limit     int
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
