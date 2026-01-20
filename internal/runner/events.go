package runner

import "time"

type EventType string

const (
	EventSelectTask    EventType = "select_task"
	EventBeadsUpdate   EventType = "beads_update"
	EventOpenCodeStart EventType = "opencode_start"
	EventOpenCodeEnd   EventType = "opencode_end"
	EventGitAdd        EventType = "git_add"
	EventGitStatus     EventType = "git_status"
	EventGitCommit     EventType = "git_commit"
	EventBeadsClose    EventType = "beads_close"
	EventBeadsVerify   EventType = "beads_verify"
	EventBeadsSync     EventType = "beads_sync"
)

type Event struct {
	Type              EventType `json:"type"`
	IssueID           string    `json:"issue_id"`
	Title             string    `json:"title"`
	Phase             string    `json:"phase"`
	ProgressCompleted int       `json:"progress_completed"`
	ProgressTotal     int       `json:"progress_total"`
	EmittedAt         time.Time `json:"emitted_at"`
}

type EventEmitter interface {
	Emit(event Event)
}
