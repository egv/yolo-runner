package workqueue

import "fmt"

type SourceRow struct {
	Source string
	State  string
	Count  int
}

func (s *Store) ListSources() ([]SourceRow, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
SELECT source, state, COUNT(*)
FROM work_items
GROUP BY source, state`)
	if err != nil {
		return nil, fmt.Errorf("list workqueue sources: %w", err)
	}
	defer rows.Close()

	var sources []SourceRow
	for rows.Next() {
		var source SourceRow
		if err := rows.Scan(&source.Source, &source.State, &source.Count); err != nil {
			return nil, fmt.Errorf("scan workqueue source row: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read workqueue source rows: %w", err)
	}
	return sources, nil
}

func (s *Store) ItemStateCounts() (map[string]int, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
SELECT state, COUNT(*)
FROM work_items
GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("count workqueue item states: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, fmt.Errorf("scan workqueue item state count: %w", err)
		}
		counts[state] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read workqueue item state counts: %w", err)
	}
	return counts, nil
}
