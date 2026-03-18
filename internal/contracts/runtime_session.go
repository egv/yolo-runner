package contracts

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type TaskSessionRuntime interface {
	Start(ctx context.Context, request TaskSessionStartRequest) (TaskSession, error)
}

type TaskSession interface {
	ID() string
	WaitReady(ctx context.Context) error
	Execute(ctx context.Context, request TaskSessionExecuteRequest) error
	Cancel(ctx context.Context, request TaskSessionCancellation) error
	Teardown(ctx context.Context, request TaskSessionTeardown) error
}

type TaskSessionStartRequest struct {
	TaskID       string
	Backend      string
	RepoRoot     string
	Command      []string
	Env          map[string]string
	LogPath      string
	ReadyTimeout time.Duration
	StopTimeout  time.Duration
	Metadata     map[string]string
}

type TaskSessionExecuteRequest struct {
	Prompt          string
	Model           string
	Mode            RunnerMode
	Metadata        map[string]string
	EventSink       TaskSessionEventSink
	ApprovalHandler TaskSessionApprovalHandler
	QuestionHandler TaskSessionQuestionHandler
}

type TaskSessionCancellation struct {
	Reason   string
	Force    bool
	Metadata map[string]string
}

type TaskSessionTeardown struct {
	Reason       string
	Force        bool
	CollectLogs  bool
	CollectFiles bool
	Metadata     map[string]string
}

type TaskSessionEventType string

const (
	TaskSessionEventTypeLifecycle        TaskSessionEventType = "lifecycle"
	TaskSessionEventTypeProgress         TaskSessionEventType = "progress"
	TaskSessionEventTypeOutput           TaskSessionEventType = "output"
	TaskSessionEventTypeLog              TaskSessionEventType = "log"
	TaskSessionEventTypeApprovalRequired TaskSessionEventType = "approval_required"
	TaskSessionEventTypeQuestionAsked    TaskSessionEventType = "question_asked"
	TaskSessionEventTypeCancellation     TaskSessionEventType = "cancellation"
	TaskSessionEventTypeTeardown         TaskSessionEventType = "teardown"
	TaskSessionEventTypeArtifact         TaskSessionEventType = "artifact"
)

type TaskSessionLifecycleState string

const (
	TaskSessionLifecycleStarting   TaskSessionLifecycleState = "starting"
	TaskSessionLifecycleReady      TaskSessionLifecycleState = "ready"
	TaskSessionLifecycleRunning    TaskSessionLifecycleState = "running"
	TaskSessionLifecycleCancelling TaskSessionLifecycleState = "cancelling"
	TaskSessionLifecycleStopped    TaskSessionLifecycleState = "stopped"
	TaskSessionLifecycleFailed     TaskSessionLifecycleState = "failed"
)

type TaskSessionEvent struct {
	Type         TaskSessionEventType
	SessionID    string
	Message      string
	Timestamp    time.Time
	Sequence     int64
	Metadata     map[string]string
	Lifecycle    *TaskSessionLifecycleEvent
	Progress     *TaskSessionProgressEvent
	Approval     *TaskSessionApprovalEvent
	Question     *TaskSessionQuestionEvent
	Cancellation *TaskSessionCancellationEvent
	Teardown     *TaskSessionTeardownEvent
	Log          *TaskSessionLogEvent
	Artifact     *TaskSessionArtifactEvent
}

type TaskSessionLifecycleEvent struct {
	State TaskSessionLifecycleState
}

type TaskSessionProgressEvent struct {
	Phase   string
	Current int
	Total   int
}

type TaskSessionLogEvent struct {
	Kind BackendLogKind
	Path string
}

type TaskSessionArtifactEvent struct {
	Name string
	Path string
	Kind string
}

type TaskSessionCancellationEvent struct {
	Reason string
	Force  bool
}

type TaskSessionTeardownEvent struct {
	Reason string
	Force  bool
}

type TaskSessionApprovalKind string

const (
	TaskSessionApprovalKindUnknown  TaskSessionApprovalKind = "unknown"
	TaskSessionApprovalKindToolCall TaskSessionApprovalKind = "tool_call"
	TaskSessionApprovalKindCommand  TaskSessionApprovalKind = "command"
	TaskSessionApprovalKindPatch    TaskSessionApprovalKind = "patch"
)

type TaskSessionApprovalOutcome string

const (
	TaskSessionApprovalApproved TaskSessionApprovalOutcome = "approved"
	TaskSessionApprovalRejected TaskSessionApprovalOutcome = "rejected"
	TaskSessionApprovalDeferred TaskSessionApprovalOutcome = "deferred"
)

type TaskSessionApprovalRequest struct {
	ID       string
	Kind     TaskSessionApprovalKind
	Title    string
	Message  string
	Command  []string
	Metadata map[string]string
	Payload  any
}

type TaskSessionApprovalDecision struct {
	Outcome  TaskSessionApprovalOutcome
	Reason   string
	Metadata map[string]string
	Payload  any
}

type TaskSessionApprovalEvent struct {
	Request  TaskSessionApprovalRequest
	Decision *TaskSessionApprovalDecision
}

type TaskSessionQuestionRequest struct {
	ID       string
	Prompt   string
	Context  string
	Options  []string
	Metadata map[string]string
	Payload  any
}

type TaskSessionQuestionResponse struct {
	Answer   string
	Metadata map[string]string
	Payload  any
}

type TaskSessionQuestionEvent struct {
	Request  TaskSessionQuestionRequest
	Response *TaskSessionQuestionResponse
}

type TaskSessionEventSink interface {
	HandleEvent(ctx context.Context, event TaskSessionEvent) error
}

type TaskSessionApprovalHandler interface {
	HandleApproval(ctx context.Context, request TaskSessionApprovalRequest) (TaskSessionApprovalDecision, error)
}

type TaskSessionQuestionHandler interface {
	HandleQuestion(ctx context.Context, request TaskSessionQuestionRequest) (TaskSessionQuestionResponse, error)
}

type TaskSessionEventSinkFunc func(ctx context.Context, event TaskSessionEvent) error

func (f TaskSessionEventSinkFunc) HandleEvent(ctx context.Context, event TaskSessionEvent) error {
	return f(ctx, event)
}

type TaskSessionApprovalHandlerFunc func(ctx context.Context, request TaskSessionApprovalRequest) (TaskSessionApprovalDecision, error)

func (f TaskSessionApprovalHandlerFunc) HandleApproval(ctx context.Context, request TaskSessionApprovalRequest) (TaskSessionApprovalDecision, error) {
	return f(ctx, request)
}

type TaskSessionQuestionHandlerFunc func(ctx context.Context, request TaskSessionQuestionRequest) (TaskSessionQuestionResponse, error)

func (f TaskSessionQuestionHandlerFunc) HandleQuestion(ctx context.Context, request TaskSessionQuestionRequest) (TaskSessionQuestionResponse, error) {
	return f(ctx, request)
}

func NormalizeTaskSessionEvent(event TaskSessionEvent) (RunnerProgress, bool) {
	timestamp := event.Timestamp.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	progressType := EventTypeRunnerProgress
	switch event.Type {
	case TaskSessionEventTypeOutput, TaskSessionEventTypeLog:
		progressType = EventTypeRunnerOutput
	case TaskSessionEventTypeApprovalRequired, TaskSessionEventTypeQuestionAsked, TaskSessionEventTypeCancellation:
		progressType = EventTypeRunnerWarning
	case TaskSessionEventTypeLifecycle, TaskSessionEventTypeProgress, TaskSessionEventTypeTeardown, TaskSessionEventTypeArtifact:
		progressType = EventTypeRunnerProgress
	default:
		if strings.TrimSpace(event.Message) == "" && len(event.Metadata) == 0 {
			return RunnerProgress{}, false
		}
	}

	metadata := cloneStringMap(event.Metadata)
	metadata = setMetadataValue(metadata, "session_id", event.SessionID)
	metadata = setMetadataValue(metadata, "sequence", strconv.FormatInt(event.Sequence, 10))
	switch event.Type {
	case TaskSessionEventTypeLifecycle:
		if event.Lifecycle != nil && strings.TrimSpace(string(event.Lifecycle.State)) != "" {
			metadata = setMetadataValue(metadata, "state", string(event.Lifecycle.State))
		}
	case TaskSessionEventTypeProgress:
		if event.Progress != nil {
			metadata = setMetadataValue(metadata, "phase", event.Progress.Phase)
			metadata = setMetadataValue(metadata, "current", strconv.Itoa(event.Progress.Current))
			metadata = setMetadataValue(metadata, "total", strconv.Itoa(event.Progress.Total))
		}
	case TaskSessionEventTypeArtifact:
		if event.Artifact != nil {
			if strings.TrimSpace(event.Artifact.Path) != "" {
				metadata = setMetadataValue(metadata, "path", event.Artifact.Path)
			}
			if strings.TrimSpace(event.Artifact.Name) != "" {
				metadata = setMetadataValue(metadata, "name", event.Artifact.Name)
			}
			if strings.TrimSpace(event.Artifact.Kind) != "" {
				metadata = setMetadataValue(metadata, "kind", event.Artifact.Kind)
			}
		}
	case TaskSessionEventTypeApprovalRequired:
		if event.Approval != nil {
			if strings.TrimSpace(string(event.Approval.Request.Kind)) != "" {
				metadata = setMetadataValue(metadata, "kind", string(event.Approval.Request.Kind))
			}
			metadata = setMetadataValue(metadata, "approval_id", event.Approval.Request.ID)
		}
	case TaskSessionEventTypeQuestionAsked:
		if event.Question != nil {
			if strings.TrimSpace(event.Question.Request.Context) != "" {
				metadata = setMetadataValue(metadata, "context", event.Question.Request.Context)
			}
			metadata = setMetadataValue(metadata, "question_id", event.Question.Request.ID)
		}
	case TaskSessionEventTypeCancellation:
		if event.Cancellation != nil {
			metadata = setMetadataValue(metadata, "reason", event.Cancellation.Reason)
			metadata = setMetadataValue(metadata, "force", strconv.FormatBool(event.Cancellation.Force))
		}
	case TaskSessionEventTypeTeardown:
		if event.Teardown != nil {
			metadata = setMetadataValue(metadata, "reason", event.Teardown.Reason)
			metadata = setMetadataValue(metadata, "force", strconv.FormatBool(event.Teardown.Force))
		}
	case TaskSessionEventTypeLog:
		if event.Log != nil {
			if strings.TrimSpace(string(event.Log.Kind)) != "" {
				metadata = setMetadataValue(metadata, "kind", string(event.Log.Kind))
			}
			if strings.TrimSpace(event.Log.Path) != "" {
				metadata = setMetadataValue(metadata, "path", event.Log.Path)
			}
		}
	}

	return RunnerProgress{
		Type:      string(progressType),
		Message:   strings.TrimSpace(event.Message),
		Metadata:  metadata,
		Timestamp: timestamp,
	}, true
}

func setMetadataValue(dst map[string]string, key string, value string) map[string]string {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return dst
	}
	if dst == nil {
		dst = map[string]string{}
	}
	dst[key] = value
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
