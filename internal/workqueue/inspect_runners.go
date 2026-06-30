package workqueue

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

type RunnerRow struct {
	ID          string
	Pid         int
	Presets     string
	Capacity    int
	StartedAt   time.Time
	HeartbeatAt time.Time
}

func (s *Store) ListRunners() ([]RunnerRow, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
SELECT id, pid, presets, capacity, started_at, heartbeat_at
FROM runners
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	defer rows.Close()

	var runners []RunnerRow
	for rows.Next() {
		var runner RunnerRow
		var startedAt string
		var heartbeatAt string
		if err := rows.Scan(
			&runner.ID,
			&runner.Pid,
			&runner.Presets,
			&runner.Capacity,
			&startedAt,
			&heartbeatAt,
		); err != nil {
			return nil, fmt.Errorf("scan runner: %w", err)
		}

		if runner.StartedAt, err = parseQueueTime("started_at", startedAt); err != nil {
			return nil, err
		}
		if runner.HeartbeatAt, err = parseQueueTime("heartbeat_at", heartbeatAt); err != nil {
			return nil, err
		}
		runners = append(runners, runner)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read runners: %w", err)
	}
	return runners, nil
}

func (s *Store) CurrentItemForRunner(runnerID string) (*workitem.Item, error) {
	if err := ensureOpenStore(s); err != nil {
		return nil, err
	}

	item, err := scanQueueItem(s.db.QueryRow(itemSelectSQL()+`
WHERE claimed_by = ?
	AND state IN (?, ?)
LIMIT 1`,
		runnerID,
		itemStateClaimed,
		"running",
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get current item for runner %q: %w", runnerID, err)
	}
	return item, nil
}
