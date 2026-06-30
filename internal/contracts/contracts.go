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

// TaskStage represents the execution stage of a task within the runner pipeline.
type TaskStage string

const (
	TaskStageIdle       TaskStage = "idle"
	TaskStageSelecting  TaskStage = "selecting"
	TaskStageRunning    TaskStage = "running"
	TaskStageCommitting TaskStage = "committing"
	TaskStageClosing    TaskStage = "closing"
	TaskStageBlocked    TaskStage = "blocked"
	TaskStageDone       TaskStage = "done"
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

	// MissingDependencyIDs contains dependency IDs referenced by tasks in the tree
	// that are unresolved outside the current snapshot.
	MissingDependencyIDs []string

	// MissingDependenciesByTask maps task IDs to dependency IDs they reference that are
	// unresolved outside the current snapshot.
	MissingDependenciesByTask map[string][]string
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

// TaskEngine handles in-memory scheduling decisions based on task graph shape
// and task state. Storage and persistence remain in StorageBackend.
type TaskEngine interface {
	BuildGraph(tree *TaskTree) (*TaskGraph, error)
	GetNextAvailable(graph *TaskGraph) []TaskSummary
	CalculateConcurrency(graph *TaskGraph, opts ConcurrencyOptions) int
	UpdateTaskStatus(graph *TaskGraph, taskID string, status TaskStatus) error
	IsComplete(graph *TaskGraph) bool
}

// TaskGraph represents a directed task graph with explicit nodes and edges.
type TaskGraph struct {
	RootID                    string
	Nodes                     map[string]*TaskNode
	Edges                     []TaskEdge
	MissingDependenciesByTask map[string][]string
}

// TaskEdge is a directed relationship from FromID -> ToID.
type TaskEdge struct {
	FromID string
	ToID   string
	Type   RelationType
}

// TaskNode stores graph topology and scheduling metadata for a single task.
type TaskNode struct {
	ID           string
	Task         Task
	Status       TaskStatus
	Parent       *TaskNode
	Children     []*TaskNode
	Dependencies []*TaskNode
	Dependents   []*TaskNode
	Depth        int
	Priority     int
}

type ConcurrencyOptions struct {
	MaxWorkers     int
	CPUCount       int
	MemoryGB       int
	TaskComplexity int
}

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
	MaxRetries int `json:"max_retries"`
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
	EventTypeRunFinished           EventType = "run_finished"
	EventTypeTaskStarted           EventType = "task_started"
	EventTypeTaskCompleted         EventType = "task_completed"
	EventTypeTaskFailed            EventType = "task_failed"
	EventTypeTaskFinished          EventType = "task_finished"
	EventTypeRunnerStarted         EventType = "runner_started"
	EventTypeRunnerFinished        EventType = "runner_finished"
	EventTypeRunnerProgress        EventType = "runner_progress"
	EventTypeRunnerHeartbeat       EventType = "runner_heartbeat"
	EventTypeRunnerCommandStarted  EventType = "runner_cmd_started"
	EventTypeRunnerCommandFinished EventType = "runner_cmd_finished"
	EventTypeRunnerOutput          EventType = "runner_output"
	EventTypeRunnerWarning         EventType = "runner_warning"
	EventTypeAgentStarted          EventType = "agent_started"
	EventTypeAgentFinished         EventType = "agent_finished"
	EventTypeAgentText             EventType = "agent_text"
	EventTypeAgentHeartbeat        EventType = "agent_heartbeat"
	EventTypeAgentProgress         EventType = "agent_progress"
	EventTypeAgentBlocked          EventType = "agent_blocked"
	EventTypeCommandRun            EventType = "command_run"
	EventTypeToolInvoked           EventType = "tool_invoked"
	EventTypeTokenUsage            EventType = "token_usage"
	EventTypeSourcePoll            EventType = "source_poll"
	EventTypeSourceHeartbeat       EventType = "source_heartbeat"
	EventTypeQueueSnapshot         EventType = "queue_snapshot"
	EventTypeRunnerRegistered      EventType = "runner_registered"
	EventTypeRunnerAlive           EventType = "runner_alive"
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

type BlockReason string

const (
	BlockReasonPermissionDenied BlockReason = "permission_denied"
	BlockReasonNoOutput         BlockReason = "no_output"
	BlockReasonRateLimited      BlockReason = "rate_limited"
	BlockReasonAuth             BlockReason = "auth"
	BlockReasonStuck            BlockReason = "stuck"
	BlockReasonOther            BlockReason = "other"
)

type Event struct {
	Type        EventType
	Proc        string
	TaskID      string
	ItemID      string
	TaskTitle   string
	WorkerID    string
	ClonePath   string
	QueuePos    int
	Priority    int
	Message     string
	Metadata    map[string]string
	Timestamp   time.Time
	Attempt     int
	RetryCount  int
	MaxAttempts int
	Source      string
	SourceRef   string
	Kind        string
	Preset      string
	RunnerID    string
	Reason      BlockReason
	Detail      string
}

type EventIdentity struct {
	Source    string
	SourceRef string
	Kind      string
	Preset    string
	RunnerID  string
}

func NewEvent(eventType EventType, identity EventIdentity) Event {
	return Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Source:    identity.Source,
		SourceRef: identity.SourceRef,
		Kind:      identity.Kind,
		Preset:    identity.Preset,
		RunnerID:  identity.RunnerID,
	}
}

// WithClonedMetadata returns a copy of the event whose Metadata map is
// private. Sinks that buffer events for asynchronous or deferred marshaling
// must call this: producers may keep mutating the map they attached after
// Emit returns, and a concurrent map iteration during JSON marshal is a
// runtime-fatal error.
func (e Event) WithClonedMetadata() Event {
	if len(e.Metadata) == 0 {
		return e
	}
	cloned := make(map[string]string, len(e.Metadata))
	for key, value := range e.Metadata {
		cloned[key] = value
	}
	e.Metadata = cloned
	return e
}

func MarshalEventJSONL(event Event) (string, error) {
	payload := struct {
		Type        EventType         `json:"type"`
		Proc        string            `json:"proc,omitempty"`
		TaskID      string            `json:"task_id"`
		ItemID      string            `json:"item_id,omitempty"`
		TaskTitle   string            `json:"task_title,omitempty"`
		WorkerID    string            `json:"worker_id,omitempty"`
		ClonePath   string            `json:"clone_path,omitempty"`
		QueuePos    int               `json:"queue_pos,omitempty"`
		Priority    int               `json:"priority,omitempty"`
		Message     string            `json:"message,omitempty"`
		Metadata    map[string]string `json:"metadata,omitempty"`
		Attempt     int               `json:"attempt,omitempty"`
		RetryCount  int               `json:"retry_count,omitempty"`
		MaxAttempts int               `json:"max_attempts,omitempty"`
		Source      string            `json:"source,omitempty"`
		SourceRef   string            `json:"source_ref,omitempty"`
		Kind        string            `json:"kind,omitempty"`
		Preset      string            `json:"preset,omitempty"`
		RunnerID    string            `json:"runner_id,omitempty"`
		Reason      BlockReason       `json:"reason,omitempty"`
		Detail      string            `json:"detail,omitempty"`
		TS          string            `json:"ts"`
	}{
		Type:        event.Type,
		Proc:        event.Proc,
		TaskID:      event.TaskID,
		ItemID:      event.ItemID,
		TaskTitle:   event.TaskTitle,
		WorkerID:    event.WorkerID,
		ClonePath:   event.ClonePath,
		QueuePos:    event.QueuePos,
		Priority:    event.Priority,
		Message:     event.Message,
		Metadata:    event.Metadata,
		Attempt:     event.Attempt,
		RetryCount:  event.RetryCount,
		MaxAttempts: event.MaxAttempts,
		Source:      event.Source,
		SourceRef:   event.SourceRef,
		Kind:        event.Kind,
		Preset:      event.Preset,
		RunnerID:    event.RunnerID,
		Reason:      event.Reason,
		Detail:      event.Detail,
		TS:          event.Timestamp.UTC().Format(time.RFC3339),
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

	// CheckoutPRBranch resolves the branch backing an existing PR (identified
	// by prID) and returns its name. The PR working tree is prepared by the
	// caller; the VCS only reports the current branch where the backend does
	// not perform the checkout itself.
	CheckoutPRBranch(ctx context.Context, prID string) (string, error)
	// PushPRBranch force-pushes the current branch to update an existing PR.
	PushPRBranch(ctx context.Context, prID string) error
}
