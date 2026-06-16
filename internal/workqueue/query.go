package workqueue

import (
	"fmt"
	"strings"
	"time"
)

// HasOpenItem reports whether this source/ref already has queued work that has
// not reached a terminal queue state.
func (s *Store) HasOpenItem(source string, sourceRef string) (bool, error) {
	if err := ensureOpenStore(s); err != nil {
		return false, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return false, fmt.Errorf("source is required")
	}
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceRef == "" {
		return false, fmt.Errorf("source ref is required")
	}

	var exists int
	if err := s.db.QueryRow(`
SELECT EXISTS (
	SELECT 1
	FROM work_items
	WHERE source = ?
		AND source_ref = ?
		AND state NOT IN (?, ?, ?)
)`,
		source,
		sourceRef,
		itemStateDone,
		itemStateFailed,
		itemStateCancelled,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check open work item for source %q ref %q: %w", source, sourceRef, err)
	}
	return exists != 0, nil
}

// PendingDepth counts currently claimable pending items for a source and set of
// presets. It mirrors Claim's eligibility checks without mutating the queue.
func (s *Store) PendingDepth(source string, presets []string) (int, error) {
	if err := ensureOpenStore(s); err != nil {
		return 0, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return 0, fmt.Errorf("source is required")
	}
	presets = normalizeClaimPresets(presets)
	if len(presets) == 0 {
		return 0, fmt.Errorf("presets are required")
	}

	presetPlaceholders := strings.TrimRight(strings.Repeat("?,", len(presets)), ",")
	query := fmt.Sprintf(`
SELECT COUNT(*)
FROM work_items wi
WHERE wi.state = ?
	AND wi.source = ?
	AND wi.preset IN (%s)
	AND (wi.not_before = '' OR wi.not_before <= ?)
	AND NOT EXISTS (
		SELECT 1
		FROM item_deps dep
		LEFT JOIN work_items dependency ON dependency.id = dep.depends_on
		WHERE dep.item_id = wi.id
			AND COALESCE(dependency.state, '') != ?
	)`, presetPlaceholders)

	args := []any{
		itemStatePending,
		source,
	}
	for _, preset := range presets {
		args = append(args, preset)
	}
	args = append(args, formatQueueTime(time.Now().UTC()), itemStateDone)

	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending work items for source %q: %w", source, err)
	}
	return count, nil
}

// ActiveDepth counts in-flight items for a source and set of presets.
func (s *Store) ActiveDepth(source string, presets []string) (int, error) {
	if err := ensureOpenStore(s); err != nil {
		return 0, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return 0, fmt.Errorf("source is required")
	}
	presets = normalizeClaimPresets(presets)
	if len(presets) == 0 {
		return 0, fmt.Errorf("presets are required")
	}

	presetPlaceholders := strings.TrimRight(strings.Repeat("?,", len(presets)), ",")
	query := fmt.Sprintf(`
SELECT COUNT(*)
FROM work_items
WHERE source = ?
	AND preset IN (%s)
	AND state IN (?, ?)`, presetPlaceholders)

	args := []any{source}
	for _, preset := range presets {
		args = append(args, preset)
	}
	args = append(args, itemStateClaimed, itemStateRunning)

	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active work items for source %q: %w", source, err)
	}
	return count, nil
}
