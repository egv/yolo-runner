package state

import "time"

type Session struct {
	ID           string
	PRID         string
	Workspace    string
	Branch       string
	Status       string
	PID          int
	Revision     string
	Heartbeat    time.Time
	FailureCount int
	LogPath      string
}
