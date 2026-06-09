package state

import "time"

type HeartbeatStatus struct {
	Fresh bool
	Stale bool
}

func HeartbeatFreshness(heartbeatAt time.Time, checkedAt time.Time, maxAge time.Duration) HeartbeatStatus {
	if heartbeatAt.IsZero() || maxAge <= 0 || checkedAt.Sub(heartbeatAt) > maxAge {
		return HeartbeatStatus{Stale: true}
	}
	return HeartbeatStatus{Fresh: true}
}
