package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type TaskStatus string

const (
	TaskStatusOpen       TaskStatus = "open"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusBlocked    TaskStatus = "blocked"
	TaskStatusClosed     TaskStatus = "closed"
	TaskStatusFailed     TaskStatus = "failed"
)

type TaskSummary struct {
	ID       string
	Title    string
	Priority *int
}

type Task struct {
	ID          string
	Title       string
	Description string
	Status      TaskStatus
	ParentID    string
	Metadata    map[string]string
}

// StorageBackend handles persistence and retrieval of task data only.
// Scheduling and dependency resolution are handled by TaskEngine.
type StorageBackend interface {
	GetTaskTree(ctx context.Context, rootID string) (*TaskTree, error)
	GetTask(ctx context.Context, taskID string) (*Task, error)
	SetTaskStatus(ctx context.Context, taskID string, status TaskStatus) error
	SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}

type TaskTree struct {
	Root      Task
	Tasks     map[string]Task
	Relations []TaskRelation
}

type TaskRelation struct {
	FromID string
	ToID   string
	Type   RelationType
}

type RelationType string

const (
	RelationParent    RelationType = "parent"
	RelationDependsOn RelationType = "depends_on"
	RelationBlocks    RelationType = "blocks"
)

type TaskManager interface {
	NextTasks(ctx context.Context, parentID string) ([]TaskSummary, error)
	GetTask(ctx context.Context, taskID string) (Task, error)
	SetTaskStatus(ctx context.Context, taskID string, status TaskStatus) error
	SetTaskData(ctx context.Context, taskID string, data map[string]string) error
}

type RunnerMode string

const (
	RunnerModeImplement RunnerMode = "implement"
	RunnerModeReview    RunnerMode = "review"
)

type RunnerRequest struct {
	TaskID     string
	ParentID   string
	Prompt     string
	Mode       RunnerMode
	Model      string
	RepoRoot   string
	Timeout    time.Duration
	Metadata   map[string]string
	OnProgress func(RunnerProgress)
}

type RunnerProgress struct {
	Type      string
	Message   string
	Metadata  map[string]string
	Timestamp time.Time
}

type RunnerResultStatus string

const (
	RunnerResultCompleted RunnerResultStatus = "completed"
	RunnerResultBlocked   RunnerResultStatus = "blocked"
	RunnerResultFailed    RunnerResultStatus = "failed"
)

var ErrInvalidRunnerResultStatus = errors.New("invalid runner result status")

type RunnerResult struct {
	Status      RunnerResultStatus
	Reason      string
	LogPath     string
	Artifacts   map[string]string
	StartedAt   time.Time
	FinishedAt  time.Time
	ReviewReady bool
}

func (r RunnerResult) Validate() error {
	switch r.Status {
	case RunnerResultCompleted, RunnerResultBlocked, RunnerResultFailed:
		return nil
	default:
		return ErrInvalidRunnerResultStatus
	}
}

type AgentRunner interface {
	Run(ctx context.Context, request RunnerRequest) (RunnerResult, error)
}

type LoopSummary struct {
	Completed int
	Blocked   int
	Failed    int
	Skipped   int
}

func (s LoopSummary) TotalProcessed() int {
	return s.Completed + s.Blocked + s.Failed + s.Skipped
}

type EventType string

const (
	EventTypeRunStarted            EventType = "run_started"
	EventTypeTaskStarted           EventType = "task_started"
	EventTypeTaskFinished          EventType = "task_finished"
	EventTypeRunnerStarted         EventType = "runner_started"
	EventTypeRunnerFinished        EventType = "runner_finished"
	EventTypeRunnerProgress        EventType = "runner_progress"
	EventTypeRunnerHeartbeat       EventType = "runner_heartbeat"
	EventTypeRunnerCommandStarted  EventType = "runner_cmd_started"
	EventTypeRunnerCommandFinished EventType = "runner_cmd_finished"
	EventTypeRunnerOutput          EventType = "runner_output"
	EventTypeRunnerWarning         EventType = "runner_warning"
	EventTypeReviewStarted         EventType = "review_started"
	EventTypeReviewFinished        EventType = "review_finished"
	EventTypeBranchCreated         EventType = "branch_created"
	EventTypeMergeQueued           EventType = "merge_queued"
	EventTypeMergeRetry            EventType = "merge_retry"
	EventTypeMergeBlocked          EventType = "merge_blocked"
	EventTypeMergeLanded           EventType = "merge_landed"
	EventTypeMergeCompleted        EventType = "merge_completed"
	EventTypePushCompleted         EventType = "push_completed"
	EventTypeTaskStatusSet         EventType = "task_status_set"
	EventTypeTaskDataUpdated       EventType = "task_data_updated"
)

type Event struct {
	Type      EventType
	TaskID    string
	TaskTitle string
	WorkerID  string
	ClonePath string
	QueuePos  int
	Message   string
	Metadata  map[string]string
	Timestamp time.Time
}

func MarshalEventJSONL(event Event) (string, error) {
	payload := struct {
		Type      EventType         `json:"type"`
		TaskID    string            `json:"task_id"`
		TaskTitle string            `json:"task_title,omitempty"`
		WorkerID  string            `json:"worker_id,omitempty"`
		ClonePath string            `json:"clone_path,omitempty"`
		QueuePos  int               `json:"queue_pos,omitempty"`
		Message   string            `json:"message,omitempty"`
		Metadata  map[string]string `json:"metadata,omitempty"`
		TS        string            `json:"ts"`
	}{
		Type:      event.Type,
		TaskID:    event.TaskID,
		TaskTitle: event.TaskTitle,
		WorkerID:  event.WorkerID,
		ClonePath: event.ClonePath,
		QueuePos:  event.QueuePos,
		Message:   event.Message,
		Metadata:  event.Metadata,
		TS:        event.Timestamp.UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

type EventSink interface {
	Emit(ctx context.Context, event Event) error
}

type AgentLoop interface {
	Run(ctx context.Context, parentID string) (LoopSummary, error)
}

type VCS interface {
	EnsureMain(ctx context.Context) error
	CreateTaskBranch(ctx context.Context, taskID string) (string, error)
	Checkout(ctx context.Context, ref string) error
	CommitAll(ctx context.Context, message string) (string, error)
	MergeToMain(ctx context.Context, sourceBranch string) error
	PushBranch(ctx context.Context, branch string) error
	PushMain(ctx context.Context) error
}
