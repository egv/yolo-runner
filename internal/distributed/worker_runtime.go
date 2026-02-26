package distributed

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const (
	QueueWorkspaceGit  = "git"
	QueueWorkspaceNone = "none"
)

type QueueTaskRef struct {
	Backend  string `json:"backend"`
	NativeID string `json:"native_id"`
}

type QueueGitWorkspaceSpec struct {
	RepoURL    string `json:"repo_url"`
	BaseRef    string `json:"base_ref,omitempty"`
	WorkBranch string `json:"work_branch,omitempty"`
}

type QueueWorkspaceSpec struct {
	Kind string                 `json:"kind"`
	Git  *QueueGitWorkspaceSpec `json:"git,omitempty"`
}

type QueueTaskRequirements struct {
	Capabilities []Capability    `json:"capabilities,omitempty"`
	Credentials  []string        `json:"credentials,omitempty"`
	Metadata     map[string]bool `json:"metadata,omitempty"`
}

type QueueTaskMessage struct {
	TaskRef       QueueTaskRef          `json:"task_ref"`
	WorkspaceSpec QueueWorkspaceSpec    `json:"workspace_spec"`
	Requirements  QueueTaskRequirements `json:"requirements,omitempty"`
	GraphRef      string                `json:"graph_ref,omitempty"`
	Request       json.RawMessage       `json:"request"`
}

type QueueWorkerCapabilitySnapshot struct {
	EnvironmentProbes ExecutorEnvironmentFeatureProbes
	CredentialFlags   map[string]bool
	ResourceHints     ExecutorResourceHints
}

type QueueWorkerRuntimeOptions struct {
	ID                      string
	InstanceID              string
	Hostname                string
	Bus                     Bus
	Runner                  contracts.AgentRunner
	Backends                map[string]contracts.AgentRunner
	AgentResolver           func(map[string]string) (string, contracts.AgentRunner, error)
	Backend                 string
	Queue                   string
	QueueConsumer           string
	QueueGroup              string
	Subjects                EventSubjects
	Capabilities            []Capability
	MaxConcurrency          int
	HeartbeatInterval       time.Duration
	CapabilityProbeInterval time.Duration
	RequestTimeout          time.Duration
	WorkspaceRoot           string
	CapabilityProbe         func(context.Context) QueueWorkerCapabilitySnapshot
	Clock                   func() time.Time
}

type QueueWorkerRuntime struct {
	id                      string
	instanceID              string
	hostname                string
	bus                     Bus
	runner                  contracts.AgentRunner
	backends                map[string]contracts.AgentRunner
	agentResolver           func(map[string]string) (string, contracts.AgentRunner, error)
	defaultBackend          string
	queue                   string
	queueConsumer           string
	queueGroup              string
	subjects                EventSubjects
	capabilities            CapabilitySet
	maxConcurrency          int
	heartbeatInterval       time.Duration
	capabilityProbeInterval time.Duration
	requestTimeout          time.Duration
	workspaceRoot           string
	capabilityProbe         func(context.Context) QueueWorkerCapabilitySnapshot
	clock                   func() time.Time

	activeLoad int64

	snapshotMu sync.RWMutex
	snapshot   QueueWorkerCapabilitySnapshot
}

func NewQueueWorkerRuntime(cfg QueueWorkerRuntimeOptions) *QueueWorkerRuntime {
	subjects := cfg.Subjects
	if subjects.Register == "" {
		subjects = DefaultEventSubjects("yolo")
	}
	if subjects.Offline == "" {
		subjects.Offline = subjects.Register
	}
	queue := strings.TrimSpace(cfg.Queue)
	if queue == "" {
		queue = "queue.tasks"
	}
	workspaceRoot := strings.TrimSpace(cfg.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = filepath.Join(os.TempDir(), "yolo-runner-worker")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	probe := cfg.CapabilityProbe
	if probe == nil {
		probe = func(context.Context) QueueWorkerCapabilitySnapshot {
			return QueueWorkerCapabilitySnapshot{
				EnvironmentProbes: DetectEnvironmentFeatureProbes(),
				CredentialFlags:   DetectCredentialPresenceFlags(),
				ResourceHints:     DetectResourceHints(),
			}
		}
	}

	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}

	return &QueueWorkerRuntime{
		id:                      strings.TrimSpace(cfg.ID),
		instanceID:              strings.TrimSpace(cfg.InstanceID),
		hostname:                strings.TrimSpace(cfg.Hostname),
		bus:                     cfg.Bus,
		runner:                  cfg.Runner,
		backends:                normalizeRunnerBackends(cfg.Backends),
		agentResolver:           cfg.AgentResolver,
		defaultBackend:          strings.TrimSpace(strings.ToLower(cfg.Backend)),
		queue:                   queue,
		queueConsumer:           strings.TrimSpace(cfg.QueueConsumer),
		queueGroup:              strings.TrimSpace(cfg.QueueGroup),
		subjects:                subjects,
		capabilities:            NewCapabilitySet(cfg.Capabilities...),
		maxConcurrency:          maxConcurrency,
		heartbeatInterval:       cfg.HeartbeatInterval,
		capabilityProbeInterval: cfg.CapabilityProbeInterval,
		requestTimeout:          cfg.RequestTimeout,
		workspaceRoot:           workspaceRoot,
		capabilityProbe:         probe,
		clock:                   clock,
	}
}

func (w *QueueWorkerRuntime) ID() string {
	if strings.TrimSpace(w.id) != "" {
		return strings.TrimSpace(w.id)
	}
	return "queue-worker-" + w.clock().Format("20060102150405.000")
}

func (w *QueueWorkerRuntime) Start(ctx context.Context) error {
	if w == nil || w.bus == nil {
		return fmt.Errorf("queue worker runtime bus is required")
	}
	if w.runner == nil && len(w.backends) == 0 {
		return fmt.Errorf("queue worker runtime runner is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(w.workspaceRoot, 0o755); err != nil {
		return err
	}

	w.refreshSnapshot(ctx)
	if err := w.publishRegistration(ctx); err != nil {
		return err
	}
	if err := w.publishHeartbeat(ctx); err != nil {
		return err
	}

	consumeOpts := QueueConsumeOptions{Consumer: w.queueConsumer, Group: w.queueGroup}
	msgCh, stopConsume, err := w.bus.ConsumeQueue(ctx, w.queue, consumeOpts)
	if err != nil {
		return err
	}
	defer stopConsume()

	heartbeatInterval := w.heartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	probeInterval := w.capabilityProbeInterval
	if probeInterval <= 0 {
		probeInterval = 30 * time.Second
	}
	probeTicker := time.NewTicker(probeInterval)
	defer probeTicker.Stop()

	slots := make(chan int, w.maxConcurrency)
	for i := 0; i < w.maxConcurrency; i++ {
		slots <- i
	}
	slotReleased := make(chan struct{}, w.maxConcurrency)

	pending := make([]QueueMessage, 0, w.maxConcurrency)

	var wg sync.WaitGroup
	dispatch := func(msg QueueMessage, slotID int) {
		wg.Add(1)
		go func(m QueueMessage, slot int) {
			defer func() {
				slots <- slot
				select {
				case slotReleased <- struct{}{}:
				default:
				}
				wg.Done()
			}()
			w.handleQueueMessage(ctx, m, slot)
		}(msg, slotID)
	}
	dispatchPending := func() {
		for len(pending) > 0 {
			select {
			case slot := <-slots:
				next := pending[0]
				pending = pending[1:]
				dispatch(next, slot)
			default:
				return
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			for _, msg := range pending {
				_ = msg.Nack(context.Background())
			}
			_ = w.publishOffline("shutdown")
			wg.Wait()
			return ctx.Err()
		case <-heartbeatTicker.C:
			if err := w.publishHeartbeat(ctx); err != nil {
				return err
			}
		case <-probeTicker.C:
			w.refreshSnapshot(ctx)
		case msg, ok := <-msgCh:
			if !ok {
				for len(pending) > 0 {
					dispatchPending()
					if len(pending) == 0 {
						break
					}
					select {
					case <-ctx.Done():
						for _, queued := range pending {
							_ = queued.Nack(context.Background())
						}
						_ = w.publishOffline("shutdown")
						wg.Wait()
						return ctx.Err()
					case <-slotReleased:
					}
				}
				wg.Wait()
				return nil
			}
			pending = append(pending, msg)
			dispatchPending()
		case <-slotReleased:
			dispatchPending()
		}
	}
}

func (w *QueueWorkerRuntime) refreshSnapshot(ctx context.Context) {
	snapshot := w.capabilityProbe(ctx)
	snapshot.CredentialFlags = cloneBoolMap(snapshot.CredentialFlags)
	w.snapshotMu.Lock()
	w.snapshot = snapshot
	w.snapshotMu.Unlock()
}

func (w *QueueWorkerRuntime) currentSnapshot() QueueWorkerCapabilitySnapshot {
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return QueueWorkerCapabilitySnapshot{
		EnvironmentProbes: w.snapshot.EnvironmentProbes,
		CredentialFlags:   cloneBoolMap(w.snapshot.CredentialFlags),
		ResourceHints:     w.snapshot.ResourceHints,
	}
}

func (w *QueueWorkerRuntime) publishRegistration(ctx context.Context) error {
	snapshot := w.currentSnapshot()
	registration := ExecutorRegistrationPayload{
		ExecutorID:              w.ID(),
		InstanceID:              w.instanceID,
		Hostname:                w.hostname,
		Capabilities:            keys(w.capabilities),
		EnvironmentProbes:       snapshot.EnvironmentProbes,
		CredentialFlags:         cloneBoolMap(snapshot.CredentialFlags),
		ResourceHints:           snapshot.ResourceHints,
		MaxConcurrency:          w.maxConcurrency,
		CapabilitySchemaVersion: CapabilitySchemaVersionV1,
		SupportedPipelines:      []string{"default"},
		SupportedAgents:         defaultSupportedAgents(w.backends, w.runner),
		StartedAt:               w.clock(),
	}
	env, err := NewEventEnvelope(EventTypeExecutorRegistered, w.ID(), "", registration)
	if err != nil {
		return err
	}
	return w.bus.Publish(ctx, w.subjects.Register, env)
}

func (w *QueueWorkerRuntime) publishHeartbeat(ctx context.Context) error {
	snapshot := w.currentSnapshot()
	currentLoad := int(atomic.LoadInt64(&w.activeLoad))
	available := w.maxConcurrency - currentLoad
	if available < 0 {
		available = 0
	}
	heartbeat := ExecutorHeartbeatPayload{
		ExecutorID:        w.ID(),
		InstanceID:        w.instanceID,
		SeenAt:            w.clock(),
		CurrentLoad:       currentLoad,
		AvailableSlots:    available,
		MaxConcurrency:    w.maxConcurrency,
		HealthStatus:      "healthy",
		EnvironmentProbes: snapshot.EnvironmentProbes,
		CredentialFlags:   cloneBoolMap(snapshot.CredentialFlags),
		ResourceHints:     snapshot.ResourceHints,
	}
	env, err := NewEventEnvelope(EventTypeExecutorHeartbeat, w.ID(), "", heartbeat)
	if err != nil {
		return err
	}
	return w.bus.Publish(ctx, w.subjects.Heartbeat, env)
}

func (w *QueueWorkerRuntime) publishOffline(reason string) error {
	env, err := NewEventEnvelope(EventTypeExecutorOffline, w.ID(), "", ExecutorOfflinePayload{
		ExecutorID: w.ID(),
		InstanceID: w.instanceID,
		SeenAt:     w.clock(),
		Reason:     strings.TrimSpace(reason),
	})
	if err != nil {
		return err
	}
	offlineCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return w.bus.Publish(offlineCtx, w.subjects.Offline, env)
}

func (w *QueueWorkerRuntime) handleQueueMessage(ctx context.Context, msg QueueMessage, slotID int) {
	payload := QueueTaskMessage{}
	if err := json.Unmarshal(msg.Event.Payload, &payload); err != nil {
		_ = msg.Ack(ctx)
		return
	}

	if !w.satisfiesRequirements(payload.Requirements) {
		_ = msg.Nack(ctx)
		return
	}

	request, err := parseQueueRunnerRequest(payload.Request)
	if err != nil {
		w.emitTaskEvent(ctx, contracts.EventTypeTaskFailed, payload, "invalid runner request")
		_ = msg.Ack(ctx)
		return
	}
	if strings.TrimSpace(request.TaskID) == "" {
		request.TaskID = strings.TrimSpace(payload.TaskRef.NativeID)
	}

	workspaceDir, cleanup, workspaceErr := w.prepareWorkspace(ctx, payload, slotID)
	if cleanup != nil {
		defer cleanup()
	}
	if workspaceErr != nil {
		w.emitTaskEvent(ctx, contracts.EventTypeTaskFailed, payload, workspaceErr.Error())
		if IsTransient(workspaceErr) {
			_ = msg.Nack(ctx)
		} else {
			_ = msg.Ack(ctx)
		}
		return
	}
	if payload.WorkspaceSpec.Kind == QueueWorkspaceGit {
		request.RepoRoot = workspaceDir
	}
	request.Metadata = mergeTaskMetadata(request.Metadata, payload)
	request.OnProgress = w.progressForwarder(ctx, request.TaskID, request.OnProgress)

	backend, selectedRunner, err := w.resolveRunner(request.Metadata)
	if err != nil {
		w.emitTaskEvent(ctx, contracts.EventTypeTaskFailed, payload, err.Error())
		_ = msg.Ack(ctx)
		return
	}
	if selectedRunner == nil {
		w.emitTaskEvent(ctx, contracts.EventTypeTaskFailed, payload, "selected runner is nil")
		_ = msg.Ack(ctx)
		return
	}

	w.emitTaskEvent(ctx, contracts.EventTypeTaskStarted, payload, "task execution started")
	atomic.AddInt64(&w.activeLoad, 1)
	result, runErr := runWithTimeout(ctx, selectedRunner, request, w.requestTimeout)
	atomic.AddInt64(&w.activeLoad, -1)

	if runErr == nil && result.Status == contracts.RunnerResultCompleted {
		w.emitTaskEvent(ctx, contracts.EventTypeTaskCompleted, payload, "task completed")
		w.emitTaskEvent(ctx, contracts.EventTypeTaskFinished, payload, "task finished")
		_ = msg.Ack(ctx)
		_ = backend
		return
	}

	failureReason := result.Reason
	if strings.TrimSpace(failureReason) == "" && runErr != nil {
		failureReason = runErr.Error()
	}
	if strings.TrimSpace(failureReason) == "" {
		failureReason = "task failed"
	}
	w.emitTaskEvent(ctx, contracts.EventTypeTaskFailed, payload, failureReason)
	if runErr != nil && IsTransient(runErr) {
		_ = msg.Nack(ctx)
		return
	}
	_ = msg.Ack(ctx)
}

func runWithTimeout(ctx context.Context, runner contracts.AgentRunner, request contracts.RunnerRequest, defaultTimeout time.Duration) (contracts.RunnerResult, error) {
	execCtx := ctx
	cancel := func() {}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	return runner.Run(execCtx, request)
}

func (w *QueueWorkerRuntime) resolveRunner(metadata map[string]string) (string, contracts.AgentRunner, error) {
	if w.agentResolver != nil {
		backend, runner, err := w.agentResolver(cloneStringMap(metadata))
		if err != nil {
			return "", nil, err
		}
		return backend, runner, nil
	}

	backend := strings.TrimSpace(strings.ToLower(metadata["backend"]))
	if backend == "" {
		backend = w.defaultBackend
	}
	if backend == "" {
		backend = "default"
	}
	if selected, ok := w.backends[backend]; ok && selected != nil {
		return backend, selected, nil
	}
	if w.runner != nil {
		return backend, w.runner, nil
	}
	if len(w.backends) == 0 {
		return backend, nil, fmt.Errorf("no runner configured for backend %q", backend)
	}
	if _, exists := w.backends[backend]; exists {
		return "", nil, fmt.Errorf("backend %q has nil runner", backend)
	}
	return backend, nil, fmt.Errorf("no runner configured for backend %q", backend)
}

func (w *QueueWorkerRuntime) prepareWorkspace(ctx context.Context, task QueueTaskMessage, slotID int) (string, func(), error) {
	slotRoot := filepath.Join(w.workspaceRoot, "slots", strconv.Itoa(slotID), sanitizePathSegment(task.TaskRef.NativeID+"-"+task.TaskRef.Backend+"-"+w.clock().Format("150405.000")))
	if err := os.MkdirAll(slotRoot, 0o755); err != nil {
		return "", nil, MarkTransient(fmt.Errorf("create slot workspace: %w", err))
	}
	cleanup := func() {
		_ = os.RemoveAll(slotRoot)
	}

	kind := strings.TrimSpace(strings.ToLower(task.WorkspaceSpec.Kind))
	if kind == "" {
		kind = QueueWorkspaceNone
	}
	switch kind {
	case QueueWorkspaceNone:
		return slotRoot, cleanup, nil
	case QueueWorkspaceGit:
		if task.WorkspaceSpec.Git == nil {
			return "", cleanup, fmt.Errorf("workspace_spec.git is required when kind is git")
		}
		repoDir, err := w.prepareGitWorkspace(ctx, slotRoot, *task.WorkspaceSpec.Git)
		if err != nil {
			return "", cleanup, err
		}
		return repoDir, cleanup, nil
	default:
		return "", cleanup, fmt.Errorf("unsupported workspace kind %q", kind)
	}
}

func (w *QueueWorkerRuntime) prepareGitWorkspace(ctx context.Context, slotRoot string, spec QueueGitWorkspaceSpec) (string, error) {
	repoURL := strings.TrimSpace(spec.RepoURL)
	if repoURL == "" {
		return "", fmt.Errorf("workspace_spec.git.repo_url is required")
	}
	repoCache := filepath.Join(w.workspaceRoot, "repos", hashString(repoURL))
	if _, err := os.Stat(repoCache); err == nil {
		if err := runGitCmd(ctx, "", "-C", repoCache, "fetch", "--all", "--prune"); err != nil {
			return "", MarkTransient(fmt.Errorf("refresh cached repo: %w", err))
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(repoCache), 0o755); err != nil {
			return "", MarkTransient(err)
		}
		if err := runGitCmd(ctx, "", "clone", "--no-hardlinks", repoURL, repoCache); err != nil {
			return "", MarkTransient(fmt.Errorf("clone repo cache: %w", err))
		}
	}

	workspaceRepo := filepath.Join(slotRoot, "repo")
	if err := runGitCmd(ctx, "", "clone", "--no-hardlinks", repoCache, workspaceRepo); err != nil {
		return "", MarkTransient(fmt.Errorf("clone workspace repo: %w", err))
	}
	if baseRef := strings.TrimSpace(spec.BaseRef); baseRef != "" {
		if err := runGitCmd(ctx, "", "-C", workspaceRepo, "checkout", baseRef); err != nil {
			return "", MarkTransient(fmt.Errorf("checkout base ref: %w", err))
		}
	}
	if branch := strings.TrimSpace(spec.WorkBranch); branch != "" {
		if err := runGitCmd(ctx, "", "-C", workspaceRepo, "checkout", "-B", branch); err != nil {
			return "", MarkTransient(fmt.Errorf("checkout work branch: %w", err))
		}
	}
	return workspaceRepo, nil
}

func runGitCmd(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (w *QueueWorkerRuntime) satisfiesRequirements(req QueueTaskRequirements) bool {
	if len(req.Capabilities) > 0 && !w.capabilities.HasAll(req.Capabilities...) {
		return false
	}
	if len(req.Credentials) == 0 {
		return true
	}
	snapshot := w.currentSnapshot()
	for _, key := range req.Credentials {
		if !snapshot.CredentialFlags[strings.TrimSpace(key)] {
			return false
		}
	}
	return true
}

func (w *QueueWorkerRuntime) emitTaskEvent(ctx context.Context, eventType contracts.EventType, task QueueTaskMessage, message string) {
	metadata := map[string]string{
		"task_backend": strings.TrimSpace(task.TaskRef.Backend),
		"task_ref":     strings.TrimSpace(task.TaskRef.NativeID),
		"graph_ref":    strings.TrimSpace(task.GraphRef),
	}
	kind := strings.TrimSpace(strings.ToLower(task.WorkspaceSpec.Kind))
	if kind == "" {
		kind = QueueWorkspaceNone
	}
	metadata["workspace_kind"] = kind
	event := contracts.Event{
		Type:      eventType,
		TaskID:    strings.TrimSpace(task.TaskRef.NativeID),
		WorkerID:  w.ID(),
		Message:   strings.TrimSpace(message),
		Metadata:  metadata,
		Timestamp: w.clock(),
	}
	w.emitMonitorEvent(ctx, event)
}

func (w *QueueWorkerRuntime) progressForwarder(ctx context.Context, taskID string, original func(contracts.RunnerProgress)) func(contracts.RunnerProgress) {
	return func(progress contracts.RunnerProgress) {
		if original != nil {
			original(progress)
		}
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = w.clock()
		}
		event := contracts.Event{
			Type:      eventTypeForRunnerProgress(progress.Type),
			TaskID:    taskID,
			WorkerID:  w.ID(),
			Message:   progress.Message,
			Metadata:  cloneStringMap(progress.Metadata),
			Timestamp: eventTime,
		}
		w.emitMonitorEvent(ctx, event)
	}
}

func (w *QueueWorkerRuntime) emitMonitorEvent(ctx context.Context, event contracts.Event) {
	if w.bus == nil || strings.TrimSpace(w.subjects.MonitorEvent) == "" {
		return
	}
	env, err := NewEventEnvelope(EventTypeMonitorEvent, w.ID(), "", MonitorEventPayload{Event: event})
	if err != nil {
		return
	}
	_ = w.bus.Publish(ctx, w.subjects.MonitorEvent, env)
}

func parseQueueRunnerRequest(raw json.RawMessage) (contracts.RunnerRequest, error) {
	type runnerTransportRequest struct {
		TaskID     string               `json:"task_id"`
		ParentID   string               `json:"parent_id"`
		Prompt     string               `json:"prompt"`
		Mode       contracts.RunnerMode `json:"mode"`
		Model      string               `json:"model"`
		RepoRoot   string               `json:"repo_root"`
		Timeout    time.Duration        `json:"timeout"`
		MaxRetries int                  `json:"max_retries"`
		Metadata   map[string]string    `json:"metadata,omitempty"`
	}
	if len(raw) == 0 {
		return contracts.RunnerRequest{}, fmt.Errorf("runner request payload is required")
	}
	transport := runnerTransportRequest{}
	if err := json.Unmarshal(raw, &transport); err != nil {
		return contracts.RunnerRequest{}, err
	}
	return contracts.RunnerRequest{
		TaskID:     transport.TaskID,
		ParentID:   transport.ParentID,
		Prompt:     transport.Prompt,
		Mode:       transport.Mode,
		Model:      transport.Model,
		RepoRoot:   transport.RepoRoot,
		Timeout:    transport.Timeout,
		MaxRetries: transport.MaxRetries,
		Metadata:   cloneStringMap(transport.Metadata),
	}, nil
}

func mergeTaskMetadata(existing map[string]string, task QueueTaskMessage) map[string]string {
	out := cloneStringMap(existing)
	if out == nil {
		out = map[string]string{}
	}
	out["task_ref.backend"] = strings.TrimSpace(task.TaskRef.Backend)
	out["task_ref.native_id"] = strings.TrimSpace(task.TaskRef.NativeID)
	if graph := strings.TrimSpace(task.GraphRef); graph != "" {
		out["graph_ref"] = graph
	}
	if kind := strings.TrimSpace(strings.ToLower(task.WorkspaceSpec.Kind)); kind != "" {
		out["workspace.kind"] = kind
	}
	return out
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		out[normalized] = value
	}
	return out
}

func hashString(value string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func sanitizePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "task"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	value = replacer.Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	value = strings.Trim(value, "-.")
	if value == "" {
		return "task"
	}
	return value
}

type transientError struct {
	err error
}

func (e transientError) Error() string {
	if e.err == nil {
		return "transient error"
	}
	return e.err.Error()
}

func (e transientError) Unwrap() error {
	return e.err
}

func MarkTransient(err error) error {
	if err == nil {
		return nil
	}
	return transientError{err: err}
}

func IsTransient(err error) bool {
	var marker transientError
	return errors.As(err, &marker)
}
