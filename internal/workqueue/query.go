package workqueue

import (
	"fmt"
	"strings"
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
