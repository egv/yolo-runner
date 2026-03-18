package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type ServiceHandler func(ctx context.Context, request ServiceRequestPayload) (ServiceResponsePayload, error)

type TaskStatusWriter interface {
	SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error
	SetTaskData(ctx context.Context, taskID string, data map[string]string) error
	GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error)
}

type TaskGraphMatcher interface {
	MatchTaskGraphFilters(filters TaskGraphSubscriptionFilter, backend string, rootID string) bool
}

type TaskGraphSource struct {
	Backend         TaskStatusWriter
	BackendType     string
	BackendInstance string
	RootIDs         []string
	SourceContext   SourceContext
	WorkspaceSpec   *WorkspaceSpec
	Requirements    []TaskRequirement
}

type MastermindOptions struct {
	ID                      string
	Bus                     Bus
	Subjects                EventSubjects
	RegistryTTL             time.Duration
	RequestTimeout          time.Duration
	Clock                   func() time.Time
	ServiceHandler          ServiceHandler
	StatusUpdateBackends    map[string]TaskStatusWriter
	StatusUpdateAuthToken   string
	TaskGraphSyncRoots      []string
	TaskGraphSources        []TaskGraphSource
	TaskGraphSyncRepoScopes []string
	TaskGraphSyncStatuses   []contracts.TaskStatus
	TaskGraphSyncLabels     []string
	TaskGraphSyncInterval   time.Duration
	SchedulerQueuePrefix    string
	SchedulerBackpressure   float64
	SchedulerTickInterval   time.Duration
}

type TaskDispatchRequest struct {
	RunnerRequest        contracts.RunnerRequest
	RequiredCapabilities []Capability
}

type Mastermind struct {
	id                      string
	bus                     Bus
	subjects                EventSubjects
	registry                *ExecutorRegistry
	requestTimeout          time.Duration
	clock                   func() time.Time
	serviceHandler          ServiceHandler
	statusUpdateBackends    map[string]TaskStatusWriter
	statusUpdateAuthToken   string
	taskGraphSyncRoots      []string
	taskGraphSources        []TaskGraphSource
	taskGraphSyncRepoScopes map[string]struct{}
	taskGraphSyncStatuses   map[contracts.TaskStatus]struct{}
	taskGraphSyncLabels     map[string]struct{}
	taskGraphSyncInterval   time.Duration
	taskGraphVersion        int64
	taskStatusMu            sync.RWMutex
	taskStatusVersions      map[string]map[string]int64
	taskStatusCommands      map[string]TaskStatusUpdateAckPayload
	taskGraphs              map[string]TaskGraphSnapshotPayload
	taskGraphsMu            sync.RWMutex
	scheduler               *mastermindScheduler
}

const (
	inboxStatusCommentKey     = "yolo.inbox.comment"
	inboxStatusMetadataPrefix = "yolo.inbox.meta."
)

func NewMastermind(cfg MastermindOptions) *Mastermind {
	subjects := cfg.Subjects
	if subjects.Register == "" {
		subjects = DefaultEventSubjects("yolo")
	}
	if subjects.Offline == "" {
		subjects.Offline = subjects.Register
	}
	statusUpdateBackends := map[string]TaskStatusWriter{}
	for rawBackend, backend := range cfg.StatusUpdateBackends {
		backendID := strings.ToLower(strings.TrimSpace(rawBackend))
		if backendID == "" {
			continue
		}
		if backend == nil {
			continue
		}
		statusUpdateBackends[backendID] = backend
	}
	taskGraphSyncRoots := canonicalFilterValues(cfg.TaskGraphSyncRoots)
	taskGraphSources := normalizeTaskGraphSources(cfg.TaskGraphSources, statusUpdateBackends, taskGraphSyncRoots)
	return &Mastermind{
		id:             strings.TrimSpace(cfg.ID),
		bus:            cfg.Bus,
		subjects:       subjects,
		registry:       NewExecutorRegistry(cfg.RegistryTTL, cfg.Clock),
		requestTimeout: cfg.RequestTimeout,
		clock: func() time.Time {
			if cfg.Clock != nil {
				return cfg.Clock().UTC()
			}
			return time.Now().UTC()
		},
		serviceHandler:          cfg.ServiceHandler,
		statusUpdateBackends:    statusUpdateBackends,
		statusUpdateAuthToken:   strings.TrimSpace(cfg.StatusUpdateAuthToken),
		taskGraphSyncRoots:      taskGraphSyncRoots,
		taskGraphSources:        taskGraphSources,
		taskGraphSyncRepoScopes: canonicalNormalizedSet(cfg.TaskGraphSyncRepoScopes),
		taskGraphSyncStatuses:   canonicalTaskStatusSet(cfg.TaskGraphSyncStatuses),
		taskGraphSyncLabels:     canonicalNormalizedSet(cfg.TaskGraphSyncLabels),
		taskGraphSyncInterval:   cfg.TaskGraphSyncInterval,
		taskStatusVersions:      make(map[string]map[string]int64),
		taskStatusCommands:      make(map[string]TaskStatusUpdateAckPayload),
		taskGraphs:              map[string]TaskGraphSnapshotPayload{},
		scheduler: newMastermindScheduler(mastermindSchedulerOptions{
			queuePrefix:      cfg.SchedulerQueuePrefix,
			backpressure:     cfg.SchedulerBackpressure,
			tickInterval:     cfg.SchedulerTickInterval,
			statusAuthToken:  strings.TrimSpace(cfg.StatusUpdateAuthToken),
			clock:            cfg.Clock,
			defaultQueueName: "queue.tasks",
		}),
	}
}

func (m *Mastermind) Registry() *ExecutorRegistry {
	return m.registry
}

func (m *Mastermind) Start(ctx context.Context) error {
	if m == nil || m.bus == nil {
		return fmt.Errorf("mastermind bus is required")
	}
	registerCh, unregister, err := m.bus.Subscribe(ctx, m.subjects.Register)
	if err != nil {
		return err
	}
	heartbeatCh, unsubscribeHeartbeat, err := m.bus.Subscribe(ctx, m.subjects.Heartbeat)
	if err != nil {
		unregister()
		return err
	}
	offlineCh, unsubscribeOffline, err := m.bus.Subscribe(ctx, m.subjects.Offline)
	if err != nil {
		unregister()
		unsubscribeHeartbeat()
		return err
	}
	serviceCh, unsubscribeService, err := m.bus.Subscribe(ctx, m.subjects.ServiceRequest)
	if err != nil {
		unregister()
		unsubscribeHeartbeat()
		unsubscribeOffline()
		return err
	}
	statusUpdateCh, unsubscribeStatusUpdate, err := m.bus.Subscribe(ctx, m.subjects.TaskStatusUpdate)
	if err != nil {
		unregister()
		unsubscribeHeartbeat()
		unsubscribeOffline()
		unsubscribeService()
		return err
	}
	taskGraphSnapshotCh, unsubscribeTaskGraphSnapshot, err := m.bus.Subscribe(ctx, m.subjects.TaskGraphSnapshot)
	if err != nil {
		unregister()
		unsubscribeHeartbeat()
		unsubscribeOffline()
		unsubscribeService()
		unsubscribeStatusUpdate()
		return err
	}
	taskGraphDiffCh, unsubscribeTaskGraphDiff, err := m.bus.Subscribe(ctx, m.subjects.TaskGraphDiff)
	if err != nil {
		unregister()
		unsubscribeHeartbeat()
		unsubscribeOffline()
		unsubscribeService()
		unsubscribeStatusUpdate()
		unsubscribeTaskGraphSnapshot()
		return err
	}
	monitorCh, unsubscribeMonitor, err := m.bus.Subscribe(ctx, m.subjects.MonitorEvent)
	if err != nil {
		unregister()
		unsubscribeHeartbeat()
		unsubscribeOffline()
		unsubscribeService()
		unsubscribeStatusUpdate()
		unsubscribeTaskGraphSnapshot()
		unsubscribeTaskGraphDiff()
		return err
	}

	go func() {
		defer unregister()
		for {
			select {
			case raw, ok := <-registerCh:
				if !ok {
					return
				}
				registration := ExecutorRegistrationPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &registration); err != nil {
					continue
				}
				m.registry.Register(registration)
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeHeartbeat()
		for {
			select {
			case raw, ok := <-heartbeatCh:
				if !ok {
					return
				}
				heartbeat := ExecutorHeartbeatPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &heartbeat); err != nil {
					continue
				}
				m.registry.Heartbeat(heartbeat)
				m.triggerScheduler(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeOffline()
		for {
			select {
			case raw, ok := <-offlineCh:
				if !ok {
					return
				}
				offline := ExecutorOfflinePayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &offline); err != nil {
					continue
				}
				m.registry.Unregister(offline)
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeService()
		for {
			select {
			case raw, ok := <-serviceCh:
				if !ok {
					return
				}
				if err := m.handleServiceRequest(ctx, raw); err != nil {
					_ = err
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeStatusUpdate()
		for {
			select {
			case raw, ok := <-statusUpdateCh:
				if !ok {
					return
				}
				if err := m.handleTaskStatusUpdate(ctx, raw); err != nil {
					_ = err
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeTaskGraphSnapshot()
		for {
			select {
			case raw, ok := <-taskGraphSnapshotCh:
				if !ok {
					return
				}
				payload := TaskGraphSnapshotPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &payload); err != nil {
					continue
				}
				m.schedulerApplySnapshot(payload)
				m.triggerScheduler(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeTaskGraphDiff()
		for {
			select {
			case raw, ok := <-taskGraphDiffCh:
				if !ok {
					return
				}
				payload := TaskGraphDiffPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &payload); err != nil {
					continue
				}
				m.schedulerApplyDiff(payload)
				m.triggerScheduler(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		defer unsubscribeMonitor()
		for {
			select {
			case raw, ok := <-monitorCh:
				if !ok {
					return
				}
				payload := MonitorEventPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &payload); err != nil {
					continue
				}
				changed := m.schedulerApplyMonitorEvent(payload.Event)
				if changed {
					m.triggerScheduler(ctx)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	go m.startTaskGraphSync(ctx)
	go m.startSchedulerLoop(ctx)

	return nil
}

func (m *Mastermind) DispatchTask(ctx context.Context, req TaskDispatchRequest) (contracts.RunnerResult, error) {
	if m == nil {
		return contracts.RunnerResult{}, fmt.Errorf("mastermind is nil")
	}
	if m.bus == nil {
		return contracts.RunnerResult{}, fmt.Errorf("mastermind bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.RunnerRequest.TaskID == "" {
		return contracts.RunnerResult{}, fmt.Errorf("task id is required")
	}
	capabilities := req.RequiredCapabilities
	if len(capabilities) == 0 {
		mode := req.RunnerRequest.Mode
		if mode == "" {
			mode = req.RunnerRequest.Mode
		}
		switch mode {
		case contracts.RunnerModeReview:
			capabilities = []Capability{CapabilityReview}
		default:
			capabilities = []Capability{CapabilityImplement}
		}
	}
	selectedExecutor, err := m.registry.Pick(capabilities...)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	correlationID := req.RunnerRequest.TaskID + "-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "")
	resultCh, unsubscribeResult, err := m.bus.Subscribe(ctx, m.subjects.TaskResult)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer unsubscribeResult()
	dispatch := TaskDispatchPayload{
		CorrelationID:        correlationID,
		TaskID:               req.RunnerRequest.TaskID,
		TargetExecutorID:     selectedExecutor.ID,
		RequiredCapabilities: capabilities,
		Request:              nil,
	}
	payloadRequest, err := requestForTransport(req.RunnerRequest)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	dispatch.Request = payloadRequest
	env, err := NewEventEnvelope(EventTypeTaskDispatch, m.id, correlationID, dispatch)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	env.CorrelationID = correlationID
	if err := m.bus.Publish(ctx, m.subjects.TaskDispatch, env); err != nil {
		return contracts.RunnerResult{}, err
	}

	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if m.requestTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(waitCtx, m.requestTimeout)
		defer cancel()
	}
	for {
		select {
		case <-waitCtx.Done():
			return contracts.RunnerResult{}, waitCtx.Err()
		case raw, ok := <-resultCh:
			if !ok {
				return contracts.RunnerResult{}, fmt.Errorf("task result channel closed")
			}
			if raw.Type != EventTypeTaskResult || raw.CorrelationID != correlationID {
				continue
			}
			payload := TaskResultPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				return contracts.RunnerResult{}, err
			}
			if strings.TrimSpace(payload.Error) != "" {
				if payload.Result.Status == "" {
					payload.Result.Status = contracts.RunnerResultFailed
				}
				return payload.Result, fmt.Errorf("%s", strings.TrimSpace(payload.Error))
			}
			return payload.Result, nil
		}
	}
}

func (m *Mastermind) handleServiceRequest(ctx context.Context, env EventEnvelope) error {
	if m == nil || m.serviceHandler == nil {
		return fmt.Errorf("service handler unavailable")
	}
	request := ServiceRequestPayload{}
	if len(env.Payload) == 0 {
		return fmt.Errorf("empty service request payload")
	}
	if err := json.Unmarshal(env.Payload, &request); err != nil {
		return err
	}
	response, err := m.serviceHandler(ctx, request)
	if err != nil {
		response.Error = err.Error()
	}
	response.RequestID = strings.TrimSpace(request.RequestID)
	response.CorrelationID = strings.TrimSpace(request.CorrelationID)
	response.ExecutorID = strings.TrimSpace(request.ExecutorID)
	response.Service = strings.TrimSpace(request.Service)
	responseEnv, err := NewEventEnvelope(EventTypeServiceResponse, m.id, response.CorrelationID, response)
	if err != nil {
		return err
	}
	responseEnv.CorrelationID = response.CorrelationID
	return m.bus.Publish(ctx, m.subjects.ServiceResult, responseEnv)
}

func requestForTransport(request contracts.RunnerRequest) (json.RawMessage, error) {
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
	transport := runnerTransportRequest{
		TaskID:     request.TaskID,
		ParentID:   request.ParentID,
		Prompt:     request.Prompt,
		Mode:       request.Mode,
		Model:      request.Model,
		RepoRoot:   request.RepoRoot,
		Timeout:    request.Timeout,
		MaxRetries: request.MaxRetries,
		Metadata:   request.Metadata,
	}
	raw, err := json.Marshal(transport)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (m *Mastermind) startTaskGraphSync(ctx context.Context) {
	if m == nil || m.bus == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sources := m.taskGraphSyncSources()
	if len(sources) == 0 {
		return
	}

	interval := m.taskGraphSyncInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	previousGraphs := map[string]TaskGraphSnapshot{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	syncTaskGraphs := func() {
		currentGraphs := map[string]TaskGraphSnapshot{}
		warnings := make([]string, 0)
		for _, source := range sources {
			for _, rootID := range source.RootIDs {
				rootID = strings.TrimSpace(rootID)
				if rootID == "" {
					continue
				}
				if len(m.taskGraphSyncRoots) > 0 && !containsString(m.taskGraphSyncRoots, rootID) {
					continue
				}
				if !matchesTaskGraphSourceRepoScope(source, m.taskGraphSyncRepoScopes) {
					continue
				}

				tree, err := source.Backend.GetTaskTree(ctx, rootID)
				if err != nil || tree == nil {
					warnings = append(warnings, fmt.Sprintf("task graph source %s/%s root %s unavailable", source.BackendType, source.BackendInstance, rootID))
					continue
				}
				graph := buildTaskGraphSnapshot(source, rootID, *tree, m.taskGraphSyncStatuses, m.taskGraphSyncLabels)
				if len(graph.Nodes) == 0 {
					continue
				}
				currentGraphs[taskGraphSourceRootKey(source, rootID)] = graph
			}
		}

		orderedGraphs := orderedTaskGraphSnapshots(currentGraphs)
		snapshot := TaskGraphSnapshotPayload{
			SchemaVersion: InboxSchemaVersionV1,
			VersionID:     atomic.AddInt64(&m.taskGraphVersion, 1),
			Graphs:        orderedGraphs,
			Warnings:      warnings,
			Metadata: map[string]string{
				"published_by": m.id,
				"published_at": m.clock().Format(time.RFC3339Nano),
			},
		}
		populateLegacyTaskGraphSnapshotFields(&snapshot)
		snapshotEnv, err := NewEventEnvelope(EventTypeTaskGraphSnapshot, m.id, "", snapshot)
		if err == nil {
			_ = m.bus.Publish(ctx, m.subjects.TaskGraphSnapshot, snapshotEnv)
		}

		graphDiffs := buildTaskGraphDiffs(previousGraphs, currentGraphs)
		if len(graphDiffs) > 0 {
			diff := TaskGraphDiffPayload{
				SchemaVersion: InboxSchemaVersionV1,
				VersionID:     atomic.AddInt64(&m.taskGraphVersion, 1),
				Graphs:        graphDiffs,
				Warnings:      warnings,
				Metadata: map[string]string{
					"published_by": m.id,
					"published_at": m.clock().Format(time.RFC3339Nano),
				},
			}
			populateLegacyTaskGraphDiffFields(&diff)
			diffEnv, err := NewEventEnvelope(EventTypeTaskGraphDiff, m.id, "", diff)
			if err == nil {
				_ = m.bus.Publish(ctx, m.subjects.TaskGraphDiff, diffEnv)
			}
		}

		previousGraphs = cloneTaskGraphSnapshotMap(currentGraphs)
	}

	syncTaskGraphs()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			syncTaskGraphs()
		}
	}
}

func (m *Mastermind) taskGraphSyncSources() []TaskGraphSource {
	if m == nil {
		return nil
	}
	out := append([]TaskGraphSource(nil), m.taskGraphSources...)
	sort.Slice(out, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(out[i].BackendType + "|" + out[i].BackendInstance + "|" + strings.Join(out[i].RootIDs, ",")))
		right := strings.ToLower(strings.TrimSpace(out[j].BackendType + "|" + out[j].BackendInstance + "|" + strings.Join(out[j].RootIDs, ",")))
		return left < right
	})
	return out
}

func (m *Mastermind) SubscribeTaskGraph(ctx context.Context, filters TaskGraphSubscriptionFilter) (<-chan TaskGraphEvent, func(), error) {
	if m == nil || m.bus == nil {
		return nil, nil, fmt.Errorf("mastermind bus is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	snapshotCh, unsubscribeSnapshot, err := m.bus.Subscribe(ctx, m.subjects.TaskGraphSnapshot)
	if err != nil {
		return nil, nil, err
	}
	diffCh, unsubscribeDiff, err := m.bus.Subscribe(ctx, m.subjects.TaskGraphDiff)
	if err != nil {
		unsubscribeSnapshot()
		return nil, nil, err
	}
	output := make(chan TaskGraphEvent, 32)
	filters.Backends = canonicalTaskStatusUpdatesBackends(filters.Backends)
	filters.RootIDs = canonicalFilterValues(filters.RootIDs)
	filters.RepoScopes = canonicalFilterValues(filters.RepoScopes)
	filters.Statuses = canonicalTaskStatusValues(filters.Statuses)
	filters.Labels = canonicalFilterValues(filters.Labels)

	closeOnce := sync.Once{}
	done := make(chan struct{})
	unsubscribe := func() {
		closeOnce.Do(func() {
			close(done)
			unsubscribeSnapshot()
			unsubscribeDiff()
		})
	}

	go func() {
		defer close(output)
		defer unsubscribe()
		for {
			select {
			case raw, ok := <-snapshotCh:
				if !ok {
					snapshotCh = nil
					if diffCh == nil {
						return
					}
					continue
				}
				payload := TaskGraphSnapshotPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &payload); err != nil {
					continue
				}
				if !matchesTaskGraphSnapshotFilter(payload, filters) {
					continue
				}
				m.taskGraphsMu.Lock()
				m.taskGraphs[taskGraphCacheKey(payload.Backend, payload.RootID)] = payload
				m.taskGraphsMu.Unlock()
				event := TaskGraphEvent{
					Type:     EventTypeTaskGraphSnapshot,
					Snapshot: &payload,
				}
				select {
				case output <- event:
				case <-done:
				default:
				}
			case raw, ok := <-diffCh:
				if !ok {
					diffCh = nil
					if snapshotCh == nil {
						return
					}
					continue
				}
				payload := TaskGraphDiffPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &payload); err != nil {
					continue
				}
				if !matchesTaskGraphDiffFilter(payload, filters) {
					continue
				}
				event := TaskGraphEvent{
					Type: EventTypeTaskGraphDiff,
					Diff: &payload,
				}
				select {
				case output <- event:
				case <-done:
				default:
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return output, unsubscribe, nil
}

func (m *Mastermind) PublishTaskStatusUpdate(ctx context.Context, req TaskStatusUpdatePayload) (string, error) {
	if m == nil || m.bus == nil {
		return "", fmt.Errorf("mastermind bus is required")
	}
	req.CommandID = strings.TrimSpace(req.CommandID)
	if req.CommandID == "" {
		req.CommandID = generateTaskStatusCommandID(m.clock)
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.Status = contracts.TaskStatus(strings.ToLower(strings.TrimSpace(string(req.Status))))
	if req.Status == "" {
		return "", fmt.Errorf("task status is required")
	}
	req.AuthToken = strings.TrimSpace(req.AuthToken)
	req.Backends = canonicalTaskStatusUpdatesBackends(req.Backends)
	req.Metadata = sanitizeTaskStatusMetadata(req.Metadata)
	env, err := NewEventEnvelope(EventTypeTaskStatusUpdate, m.id, req.CommandID, req)
	if err != nil {
		return "", err
	}
	env.CorrelationID = req.CommandID
	return req.CommandID, m.bus.Publish(ctx, m.subjects.TaskStatusUpdate, env)
}

func (m *Mastermind) PublishTaskStatusUpdateCommand(ctx context.Context, req TaskStatusUpdateCommandPayload) (string, error) {
	return m.PublishTaskStatusUpdate(ctx, req)
}

func (m *Mastermind) AckStatusUpdate(ctx context.Context, payload TaskStatusUpdateAckPayload) error {
	if m == nil || m.bus == nil {
		return fmt.Errorf("mastermind bus is required")
	}
	payload.CommandID = strings.TrimSpace(payload.CommandID)
	payload.TaskID = strings.TrimSpace(payload.TaskID)
	payload.Backends = canonicalTaskStatusUpdatesBackends(payload.Backends)
	if payload.Result == "" {
		if payload.Success {
			payload.Result = "ok"
		} else {
			payload.Result = "error"
		}
	}
	if payload.Result == "ok" {
		payload.Success = true
	}
	if !payload.Success && payload.Reason == "" {
		payload.Reason = strings.TrimSpace(payload.Message)
	}
	env, err := NewEventEnvelope(EventTypeTaskStatusAck, m.id, payload.CommandID, payload)
	if err != nil {
		return err
	}
	env.CorrelationID = payload.CommandID
	return m.bus.Publish(ctx, m.subjects.TaskStatusUpdateAck, env)
}

func (m *Mastermind) RejectStatusUpdate(ctx context.Context, payload TaskStatusUpdateRejectPayload) error {
	if m == nil || m.bus == nil {
		return fmt.Errorf("mastermind bus is required")
	}
	payload.CommandID = strings.TrimSpace(payload.CommandID)
	payload.TaskID = strings.TrimSpace(payload.TaskID)
	payload.Backends = canonicalTaskStatusUpdatesBackends(payload.Backends)
	env, err := NewEventEnvelope(EventTypeTaskStatusReject, m.id, payload.CommandID, payload)
	if err != nil {
		return err
	}
	env.CorrelationID = payload.CommandID
	return m.bus.Publish(ctx, m.subjects.TaskStatusUpdateReject, env)
}

func (m *Mastermind) handleTaskStatusUpdate(ctx context.Context, env EventEnvelope) error {
	if m == nil || len(m.statusUpdateBackends) == 0 {
		return fmt.Errorf("task status update backends are not configured")
	}
	request := TaskStatusUpdatePayload{}
	if len(env.Payload) == 0 {
		return fmt.Errorf("empty task status update payload")
	}
	if err := json.Unmarshal(env.Payload, &request); err != nil {
		return err
	}

	request.CommandID = strings.TrimSpace(request.CommandID)
	request.TaskID = strings.TrimSpace(request.TaskID)
	request.AuthToken = strings.TrimSpace(request.AuthToken)
	request.Status = contracts.TaskStatus(strings.TrimSpace(string(request.Status)))
	request.Backends = canonicalTaskStatusUpdatesBackends(request.Backends)
	request.Metadata = sanitizeTaskStatusMetadata(request.Metadata)
	if request.CommandID == "" {
		request.CommandID = env.CorrelationID
		if request.CommandID == "" {
			request.CommandID = generateTaskStatusCommandID(m.clock)
		}
	}
	m.taskStatusMu.RLock()
	if prior, ok := m.taskStatusCommands[request.CommandID]; ok {
		m.taskStatusMu.RUnlock()
		return m.AckStatusUpdate(ctx, prior)
	}
	m.taskStatusMu.RUnlock()

	if request.TaskID == "" {
		return fmt.Errorf("task id is required for task status update")
	}
	if request.Status == "" {
		return m.rejectStatusUpdate(ctx, request, "missing status")
	}
	if m.statusUpdateAuthToken != "" && request.AuthToken != m.statusUpdateAuthToken {
		return m.rejectStatusUpdate(ctx, request, "missing or invalid auth token")
	}

	backends := request.Backends
	if len(backends) == 0 {
		for backendID := range m.statusUpdateBackends {
			backends = append(backends, backendID)
		}
		backends = canonicalTaskStatusUpdatesBackends(backends)
	}
	if len(backends) == 0 {
		return m.rejectStatusUpdate(ctx, request, "no configured task backends")
	}

	currentVersions := map[string]int64{}
	m.taskStatusMu.Lock()
	for _, backendID := range backends {
		backend, ok := m.statusUpdateBackends[backendID]
		if !ok || backend == nil {
			m.taskStatusMu.Unlock()
			return m.rejectStatusUpdate(ctx, request, "unknown task backend "+backendID)
		}
		versionsForBackend, ok := m.taskStatusVersions[backendID]
		if !ok {
			versionsForBackend = map[string]int64{}
			m.taskStatusVersions[backendID] = versionsForBackend
		}
		currentVersions[backendID] = versionsForBackend[request.TaskID]
		if request.ExpectedVersion > 0 && currentVersions[backendID] != request.ExpectedVersion {
			m.taskStatusMu.Unlock()
			return m.rejectStatusUpdate(ctx, request, fmt.Sprintf("version conflict for backend %s", backendID))
		}
		_ = backend
	}

	newVersions := map[string]int64{}
	for _, backendID := range backends {
		backend := m.statusUpdateBackends[backendID]
		if err := backend.SetTaskStatus(ctx, request.TaskID, request.Status); err != nil {
			m.taskStatusMu.Unlock()
			return m.rejectStatusUpdate(ctx, request, "failed to update task status: "+err.Error())
		}
		commentPayload := map[string]string{}
		for key, value := range request.Metadata {
			commentPayload[inboxStatusMetadataPrefix+key] = strings.TrimSpace(value)
		}
		if strings.TrimSpace(request.Comment) != "" {
			commentPayload[inboxStatusCommentKey] = strings.TrimSpace(request.Comment)
		}
		if len(commentPayload) > 0 {
			if err := backend.SetTaskData(ctx, request.TaskID, commentPayload); err != nil {
				m.taskStatusMu.Unlock()
				return m.rejectStatusUpdate(ctx, request, "failed to write status comment: "+err.Error())
			}
		}
		current := currentVersions[backendID]
		newVersions[backendID] = current + 1
		m.taskStatusVersions[backendID][request.TaskID] = current + 1
	}
	m.taskStatusMu.Unlock()

	ack := TaskStatusUpdateAckPayload{
		TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
			CommandID: request.CommandID,
			TaskID:    request.TaskID,
			Status:    request.Status,
			Backends:  backends,
			Versions:  newVersions,
			Result:    "ok",
			Success:   true,
		},
	}
	m.taskStatusMu.Lock()
	m.taskStatusCommands[request.CommandID] = ack
	m.taskStatusMu.Unlock()
	return m.AckStatusUpdate(ctx, ack)
}

func (m *Mastermind) rejectStatusUpdate(_ context.Context, request TaskStatusUpdatePayload, reason string) error {
	if m == nil {
		return fmt.Errorf("mastermind unavailable: %s", reason)
	}
	ack := TaskStatusUpdateAckPayload{
		TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
			CommandID: request.CommandID,
			TaskID:    request.TaskID,
			Status:    request.Status,
			Backends:  request.Backends,
			Result:    "error",
			Message:   reason,
			Reason:    reason,
			Success:   false,
		},
	}
	if request.CommandID != "" {
		m.taskStatusMu.Lock()
		if existing, ok := m.taskStatusCommands[request.CommandID]; ok {
			ack = existing
		} else {
			m.taskStatusCommands[request.CommandID] = ack
		}
		m.taskStatusMu.Unlock()
	}
	_ = m.AckStatusUpdate(context.Background(), ack)
	return m.RejectStatusUpdate(context.Background(), TaskStatusUpdateRejectPayload{
		TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
			CommandID: request.CommandID,
			TaskID:    request.TaskID,
			Status:    request.Status,
			Backends:  request.Backends,
		},
		Reason: reason,
	})
}

func taskGraphCacheKey(backend, rootID string) string {
	return strings.ToLower(strings.TrimSpace(backend)) + "|" + strings.TrimSpace(rootID)
}

func normalizeTaskGraphSources(configured []TaskGraphSource, statusBackends map[string]TaskStatusWriter, defaultRoots []string) []TaskGraphSource {
	if len(configured) > 0 {
		out := make([]TaskGraphSource, 0, len(configured))
		for _, source := range configured {
			if source.Backend == nil {
				continue
			}
			normalized := source
			normalized.BackendType = strings.ToLower(strings.TrimSpace(normalized.BackendType))
			normalized.BackendInstance = strings.TrimSpace(normalized.BackendInstance)
			if normalized.BackendType == "" {
				normalized.BackendType = normalized.BackendInstance
			}
			if normalized.BackendInstance == "" {
				normalized.BackendInstance = normalized.BackendType
			}
			if len(normalized.RootIDs) == 0 {
				normalized.RootIDs = append([]string{}, defaultRoots...)
			}
			normalized.RootIDs = canonicalFilterValues(normalized.RootIDs)
			normalized.Requirements = cloneTaskRequirements(normalized.Requirements)
			normalized.WorkspaceSpec = cloneWorkspaceSpec(normalized.WorkspaceSpec)
			normalized.SourceContext = normalizedTaskSourceContext(normalized.SourceContext)
			if len(normalized.RootIDs) == 0 {
				continue
			}
			out = append(out, normalized)
		}
		return out
	}
	if len(defaultRoots) == 0 {
		return nil
	}
	out := make([]TaskGraphSource, 0, len(statusBackends))
	for backendID, backend := range statusBackends {
		if backend == nil {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(backendID))
		if normalized == "" {
			continue
		}
		out = append(out, TaskGraphSource{
			Backend:         backend,
			BackendType:     normalized,
			BackendInstance: normalized,
			RootIDs:         append([]string{}, defaultRoots...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].BackendInstance < out[j].BackendInstance
	})
	return out
}

func canonicalNormalizedSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func canonicalTaskStatusSet(statuses []contracts.TaskStatus) map[contracts.TaskStatus]struct{} {
	out := map[contracts.TaskStatus]struct{}{}
	for _, status := range statuses {
		normalized := contracts.TaskStatus(strings.ToLower(strings.TrimSpace(string(status))))
		if normalized == "" {
			continue
		}
		out[normalized] = struct{}{}
	}
	return out
}

func canonicalTaskStatusValues(statuses []contracts.TaskStatus) []contracts.TaskStatus {
	set := canonicalTaskStatusSet(statuses)
	if len(set) == 0 {
		return nil
	}
	values := make([]contracts.TaskStatus, 0, len(set))
	for status := range set {
		values = append(values, status)
	}
	sort.Slice(values, func(i, j int) bool {
		return strings.Compare(string(values[i]), string(values[j])) < 0
	})
	return values
}

func matchesTaskGraphSourceRepoScope(source TaskGraphSource, scopes map[string]struct{}) bool {
	if len(scopes) == 0 {
		return true
	}
	candidates := []string{
		strings.ToLower(strings.TrimSpace(source.SourceContext.Repository)),
		normalizeRepositoryScope(source.WorkspaceSpec),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := scopes[candidate]; ok {
			return true
		}
	}
	return false
}

func normalizeRepositoryScope(spec *WorkspaceSpec) string {
	if spec == nil {
		return ""
	}
	raw := strings.TrimSpace(spec.RepoURL)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil {
		path := strings.Trim(strings.TrimSpace(parsed.Path), "/")
		path = strings.TrimSuffix(path, ".git")
		return strings.ToLower(path)
	}
	return strings.ToLower(strings.Trim(strings.TrimSuffix(raw, ".git"), "/"))
}

func buildTaskGraphSnapshot(source TaskGraphSource, rootID string, tree contracts.TaskTree, statusFilters map[contracts.TaskStatus]struct{}, labelFilters map[string]struct{}) TaskGraphSnapshot {
	taskIDs := make([]string, 0, len(tree.Tasks))
	for taskID := range tree.Tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	nodes := make([]TaskGraphNode, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task := tree.Tasks[taskID]
		if !matchesTaskGraphTaskFilters(task, statusFilters, labelFilters) {
			continue
		}
		node := TaskGraphNode{
			TaskID:       strings.TrimSpace(task.ID),
			ParentTaskID: strings.TrimSpace(task.ParentID),
			Title:        strings.TrimSpace(task.Title),
			Status:       task.Status,
			GraphRef:     strings.TrimSpace(rootID),
			TaskRef: TaskRef{
				BackendInstance: strings.TrimSpace(source.BackendInstance),
				BackendType:     strings.TrimSpace(source.BackendType),
				BackendNativeID: strings.TrimSpace(task.ID),
			},
			SourceContext: normalizedTaskSourceContext(source.SourceContext),
			WorkspaceSpec: cloneWorkspaceSpec(source.WorkspaceSpec),
			Requirements:  cloneTaskRequirements(source.Requirements),
			Metadata:      sanitizeTaskStatusMetadata(task.Metadata),
		}
		if node.TaskRef.BackendType == "" {
			node.TaskRef.BackendType = node.TaskRef.BackendInstance
		}
		if node.TaskRef.BackendInstance == "" {
			node.TaskRef.BackendInstance = node.TaskRef.BackendType
		}
		nodes = append(nodes, node)
	}
	return TaskGraphSnapshot{
		GraphRef:      strings.TrimSpace(rootID),
		SourceContext: normalizedTaskSourceContext(source.SourceContext),
		Nodes:         nodes,
	}
}

func matchesTaskGraphTaskFilters(task contracts.Task, statusFilters map[contracts.TaskStatus]struct{}, labelFilters map[string]struct{}) bool {
	if len(statusFilters) > 0 {
		if _, ok := statusFilters[contracts.TaskStatus(strings.ToLower(strings.TrimSpace(string(task.Status))))]; !ok {
			return false
		}
	}
	if len(labelFilters) > 0 {
		labels := taskLabels(task)
		hasLabel := false
		for _, label := range labels {
			if _, ok := labelFilters[label]; ok {
				hasLabel = true
				break
			}
		}
		if !hasLabel {
			return false
		}
	}
	return true
}

func taskLabels(task contracts.Task) []string {
	if len(task.Metadata) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	labels := make([]string, 0)
	for key, value := range task.Metadata {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		normalizedValue := strings.TrimSpace(value)
		switch {
		case normalizedKey == "labels", normalizedKey == "label", strings.Contains(normalizedKey, "label"):
			for _, part := range strings.Split(normalizedValue, ",") {
				label := strings.ToLower(strings.TrimSpace(part))
				if label == "" {
					continue
				}
				if _, exists := seen[label]; exists {
					continue
				}
				seen[label] = struct{}{}
				labels = append(labels, label)
			}
		}
	}
	sort.Strings(labels)
	return labels
}

func taskGraphSourceRootKey(source TaskGraphSource, rootID string) string {
	return strings.ToLower(strings.TrimSpace(source.BackendInstance)) + "|" + strings.TrimSpace(rootID)
}

func orderedTaskGraphSnapshots(graphs map[string]TaskGraphSnapshot) []TaskGraphSnapshot {
	if len(graphs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(graphs))
	for key := range graphs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]TaskGraphSnapshot, 0, len(keys))
	for _, key := range keys {
		out = append(out, cloneTaskGraphSnapshot(graphs[key]))
	}
	return out
}

func cloneTaskGraphSnapshotMap(in map[string]TaskGraphSnapshot) map[string]TaskGraphSnapshot {
	if len(in) == 0 {
		return map[string]TaskGraphSnapshot{}
	}
	out := make(map[string]TaskGraphSnapshot, len(in))
	for key, graph := range in {
		out[key] = cloneTaskGraphSnapshot(graph)
	}
	return out
}

func cloneTaskGraphSnapshot(graph TaskGraphSnapshot) TaskGraphSnapshot {
	cloned := graph
	cloned.SourceContext = normalizedTaskSourceContext(graph.SourceContext)
	cloned.Nodes = append([]TaskGraphNode{}, graph.Nodes...)
	for i := range cloned.Nodes {
		cloned.Nodes[i].SourceContext = normalizedTaskSourceContext(cloned.Nodes[i].SourceContext)
		cloned.Nodes[i].WorkspaceSpec = cloneWorkspaceSpec(cloned.Nodes[i].WorkspaceSpec)
		cloned.Nodes[i].Requirements = cloneTaskRequirements(cloned.Nodes[i].Requirements)
		cloned.Nodes[i].Metadata = sanitizeTaskStatusMetadata(cloned.Nodes[i].Metadata)
	}
	return cloned
}

func cloneWorkspaceSpec(spec *WorkspaceSpec) *WorkspaceSpec {
	if spec == nil {
		return nil
	}
	cloned := *spec
	cloned.Kind = strings.TrimSpace(cloned.Kind)
	cloned.RepoURL = strings.TrimSpace(cloned.RepoURL)
	cloned.Ref = strings.TrimSpace(cloned.Ref)
	return &cloned
}

func cloneTaskRequirements(requirements []TaskRequirement) []TaskRequirement {
	if len(requirements) == 0 {
		return nil
	}
	out := make([]TaskRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		name := strings.TrimSpace(requirement.Name)
		if name == "" {
			continue
		}
		out = append(out, TaskRequirement{
			Name:   name,
			Kind:   strings.TrimSpace(requirement.Kind),
			Detail: strings.TrimSpace(requirement.Detail),
		})
	}
	return out
}

func normalizedTaskSourceContext(ctx SourceContext) SourceContext {
	return SourceContext{
		Provider:     strings.ToLower(strings.TrimSpace(ctx.Provider)),
		Repository:   strings.TrimSpace(ctx.Repository),
		Organization: strings.TrimSpace(ctx.Organization),
		Project:      strings.TrimSpace(ctx.Project),
	}
}

func populateLegacyTaskGraphSnapshotFields(payload *TaskGraphSnapshotPayload) {
	if payload == nil || len(payload.Graphs) != 1 {
		return
	}
	graph := payload.Graphs[0]
	payload.RootID = strings.TrimSpace(graph.GraphRef)
	if len(graph.Nodes) > 0 {
		payload.Backend = strings.TrimSpace(graph.Nodes[0].TaskRef.BackendType)
	}
}

func populateLegacyTaskGraphDiffFields(payload *TaskGraphDiffPayload) {
	if payload == nil || len(payload.Graphs) != 1 {
		return
	}
	graph := payload.Graphs[0]
	payload.RootID = strings.TrimSpace(graph.GraphRef)
	if len(graph.UpsertNodes) > 0 {
		payload.Backend = strings.TrimSpace(graph.UpsertNodes[0].TaskRef.BackendType)
	}
	if payload.Backend == "" {
		payload.Backend = strings.ToLower(strings.TrimSpace(graph.SourceContext.Provider))
	}
	if len(payload.Changes) == 0 {
		if len(graph.ChangedFields) > 0 {
			payload.Changes = append([]string{}, graph.ChangedFields...)
		} else if len(graph.UpsertNodes) > 0 || len(graph.DeleteTaskIDs) > 0 {
			payload.Changes = []string{"task-graph-updated"}
		}
	}
}

func buildTaskGraphDiffs(previous map[string]TaskGraphSnapshot, current map[string]TaskGraphSnapshot) []TaskGraphDiff {
	diffs := make([]TaskGraphDiff, 0)
	for key, currentGraph := range current {
		previousGraph, exists := previous[key]
		if !exists {
			diffs = append(diffs, TaskGraphDiff{
				GraphRef:      currentGraph.GraphRef,
				SourceContext: currentGraph.SourceContext,
				UpsertNodes:   append([]TaskGraphNode{}, currentGraph.Nodes...),
				ChangedFields: []string{"graph_added"},
			})
			continue
		}
		upsert, deleted := diffTaskGraphNodes(previousGraph.Nodes, currentGraph.Nodes)
		if len(upsert) == 0 && len(deleted) == 0 {
			continue
		}
		changedFields := []string{"nodes"}
		if len(upsert) > 0 {
			changedFields = append(changedFields, "upsert_nodes")
		}
		if len(deleted) > 0 {
			changedFields = append(changedFields, "delete_task_ids")
		}
		sort.Strings(changedFields)
		diffs = append(diffs, TaskGraphDiff{
			GraphRef:      currentGraph.GraphRef,
			SourceContext: currentGraph.SourceContext,
			UpsertNodes:   upsert,
			DeleteTaskIDs: deleted,
			ChangedFields: changedFields,
		})
	}
	for key, previousGraph := range previous {
		if _, exists := current[key]; exists {
			continue
		}
		deleted := make([]string, 0, len(previousGraph.Nodes))
		for _, node := range previousGraph.Nodes {
			deleted = append(deleted, strings.TrimSpace(node.TaskID))
		}
		sort.Strings(deleted)
		diffs = append(diffs, TaskGraphDiff{
			GraphRef:      previousGraph.GraphRef,
			SourceContext: previousGraph.SourceContext,
			DeleteTaskIDs: deleted,
			ChangedFields: []string{"graph_removed", "delete_task_ids"},
		})
	}
	sort.Slice(diffs, func(i, j int) bool {
		left := diffs[i].GraphRef + "|" + diffs[i].SourceContext.Repository + "|" + diffs[i].SourceContext.Project
		right := diffs[j].GraphRef + "|" + diffs[j].SourceContext.Repository + "|" + diffs[j].SourceContext.Project
		return left < right
	})
	return diffs
}

func diffTaskGraphNodes(previous []TaskGraphNode, current []TaskGraphNode) ([]TaskGraphNode, []string) {
	previousByID := map[string]string{}
	currentByID := map[string]TaskGraphNode{}
	for _, node := range previous {
		previousByID[strings.TrimSpace(node.TaskID)] = taskGraphNodeSignature(node)
	}
	for _, node := range current {
		currentByID[strings.TrimSpace(node.TaskID)] = node
	}
	upsert := make([]TaskGraphNode, 0)
	for taskID, node := range currentByID {
		signature := taskGraphNodeSignature(node)
		if previousSig, ok := previousByID[taskID]; ok && previousSig == signature {
			continue
		}
		upsert = append(upsert, node)
	}
	deleted := make([]string, 0)
	for taskID := range previousByID {
		if _, ok := currentByID[taskID]; ok {
			continue
		}
		deleted = append(deleted, taskID)
	}
	sort.Slice(upsert, func(i, j int) bool {
		return upsert[i].TaskID < upsert[j].TaskID
	})
	sort.Strings(deleted)
	return upsert, deleted
}

func taskGraphNodeSignature(node TaskGraphNode) string {
	raw, err := json.Marshal(node)
	if err != nil {
		return strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	}
	return string(raw)
}

func cloneTaskTree(tree *contracts.TaskTree) *contracts.TaskTree {
	if tree == nil {
		return nil
	}
	cloned := *tree
	cloned.Root.Metadata = map[string]string{}
	for key, value := range tree.Root.Metadata {
		cloned.Root.Metadata[strings.TrimSpace(key)] = value
	}
	cloned.Tasks = make(map[string]contracts.Task, len(tree.Tasks))
	for taskID, task := range tree.Tasks {
		clonedTask := task
		clonedTask.Metadata = map[string]string{}
		for key, value := range task.Metadata {
			clonedTask.Metadata[strings.TrimSpace(key)] = value
		}
		cloned.Tasks[taskID] = clonedTask
	}
	if len(tree.Relations) > 0 {
		cloned.Relations = make([]contracts.TaskRelation, 0, len(tree.Relations))
		cloned.Relations = append(cloned.Relations, tree.Relations...)
	}
	if len(tree.MissingDependencyIDs) > 0 {
		cloned.MissingDependencyIDs = append([]string{}, tree.MissingDependencyIDs...)
	}
	if len(tree.MissingDependenciesByTask) > 0 {
		cloned.MissingDependenciesByTask = map[string][]string{}
		for taskID, deps := range tree.MissingDependenciesByTask {
			cloned.MissingDependenciesByTask[taskID] = append([]string{}, deps...)
		}
	}
	return &cloned
}

func buildTaskGraphDiffChanges(previous contracts.TaskTree, current contracts.TaskTree) []string {
	changes := make([]string, 0)
	seen := map[string]struct{}{}
	for taskID, previousTask := range previous.Tasks {
		currentTask, ok := current.Tasks[taskID]
		if !ok {
			changes = append(changes, "removed:"+taskID)
			continue
		}
		if previousTask.Status != currentTask.Status ||
			previousTask.ParentID != currentTask.ParentID ||
			previousTask.Title != currentTask.Title ||
			buildTaskMetadataSignature(previousTask.Metadata) != buildTaskMetadataSignature(currentTask.Metadata) {
			changes = append(changes, "task:"+taskID)
			seen[taskID] = struct{}{}
		}
	}
	for taskID := range current.Tasks {
		if _, ok := previous.Tasks[taskID]; !ok {
			changes = append(changes, "task:"+taskID)
			seen[taskID] = struct{}{}
		} else if _, ok := seen[taskID]; ok {
			continue
		}
	}
	if buildTaskRelationSignature(previous.Relations) != buildTaskRelationSignature(current.Relations) {
		changes = append(changes, "relations")
	}
	if len(changes) == 0 && previous.Root.ID == current.Root.ID {
		return nil
	}
	sort.Strings(changes)
	return changes
}

func buildTaskTreeSignature(tree *contracts.TaskTree) string {
	if tree == nil {
		return ""
	}
	parts := make([]string, 0, 4+len(tree.Tasks)+len(tree.Relations))
	parts = append(parts, "root:"+tree.Root.ID+"|"+string(tree.Root.Status)+"|"+buildTaskMetadataSignature(tree.Root.Metadata))
	taskIDs := make([]string, 0, len(tree.Tasks))
	for taskID := range tree.Tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		task := tree.Tasks[taskID]
		parts = append(parts, "task:"+task.ID+"|"+task.Title+"|"+string(task.Status)+"|"+buildTaskMetadataSignature(task.Metadata))
	}
	relationSignatures := buildTaskRelationSignature(tree.Relations)
	if relationSignatures != "" {
		parts = append(parts, relationSignatures)
	}
	return strings.Join(parts, "\n")
}

func buildTaskMetadataSignature(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+metadata[key])
	}
	return strings.Join(parts, "|")
}

func buildTaskRelationSignature(relations []contracts.TaskRelation) string {
	relationSignatures := make([]string, 0, len(relations))
	for _, relation := range relations {
		fromID := strings.TrimSpace(relation.FromID)
		toID := strings.TrimSpace(relation.ToID)
		if fromID == "" || toID == "" {
			continue
		}
		relationSignatures = append(relationSignatures, string(relation.Type)+"|"+fromID+"|"+toID)
	}
	sort.Strings(relationSignatures)
	return strings.Join(relationSignatures, ",")
}

func matchesTaskGraphFilter(backend string, rootID string, filters TaskGraphSubscriptionFilter) bool {
	backend = strings.ToLower(strings.TrimSpace(backend))
	rootID = strings.TrimSpace(rootID)

	if len(filters.Backends) > 0 && !containsString(filters.Backends, backend) {
		return false
	}
	if len(filters.RootIDs) > 0 && !containsString(filters.RootIDs, rootID) {
		return false
	}
	return true
}

func matchesTaskGraphSnapshotFilter(payload TaskGraphSnapshotPayload, filters TaskGraphSubscriptionFilter) bool {
	graphs, err := payload.NormalizeGraphs()
	if err == nil && len(graphs) > 0 {
		for _, graph := range graphs {
			if matchesTaskGraphSnapshotGraphFilter(payload, graph, filters) {
				return true
			}
		}
		return false
	}
	return matchesTaskGraphFilter(payload.Backend, payload.RootID, filters)
}

func matchesTaskGraphDiffFilter(payload TaskGraphDiffPayload, filters TaskGraphSubscriptionFilter) bool {
	if len(payload.Graphs) > 0 {
		for _, graph := range payload.Graphs {
			if matchesTaskGraphDiffGraphFilter(payload, graph, filters) {
				return true
			}
		}
		return false
	}
	return matchesTaskGraphFilter(payload.Backend, payload.RootID, filters)
}

func matchesTaskGraphSnapshotGraphFilter(payload TaskGraphSnapshotPayload, graph TaskGraphSnapshot, filters TaskGraphSubscriptionFilter) bool {
	rootID := strings.TrimSpace(graph.GraphRef)
	if rootID == "" {
		rootID = strings.TrimSpace(payload.RootID)
	}
	backend := strings.ToLower(strings.TrimSpace(payload.Backend))
	if len(graph.Nodes) > 0 {
		backend = strings.ToLower(strings.TrimSpace(graph.Nodes[0].TaskRef.BackendType))
	}
	if !matchesTaskGraphFilter(backend, rootID, filters) {
		return false
	}
	if len(filters.RepoScopes) > 0 {
		matched := false
		for _, scope := range filters.RepoScopes {
			scope = strings.ToLower(strings.TrimSpace(scope))
			if scope == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(graph.SourceContext.Repository), scope) {
				matched = true
				break
			}
			for _, node := range graph.Nodes {
				if strings.EqualFold(strings.TrimSpace(node.SourceContext.Repository), scope) || strings.EqualFold(normalizeRepositoryScope(node.WorkspaceSpec), scope) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	if len(filters.Statuses) == 0 && len(filters.Labels) == 0 {
		return true
	}
	statusFilter := canonicalTaskStatusSet(filters.Statuses)
	labelFilter := canonicalNormalizedSet(filters.Labels)
	for _, node := range graph.Nodes {
		task := contracts.Task{
			Status:   node.Status,
			Metadata: node.Metadata,
		}
		if matchesTaskGraphTaskFilters(task, statusFilter, labelFilter) {
			return true
		}
	}
	return false
}

func matchesTaskGraphDiffGraphFilter(payload TaskGraphDiffPayload, graph TaskGraphDiff, filters TaskGraphSubscriptionFilter) bool {
	rootID := strings.TrimSpace(graph.GraphRef)
	if rootID == "" {
		rootID = strings.TrimSpace(payload.RootID)
	}
	backend := strings.ToLower(strings.TrimSpace(payload.Backend))
	if len(graph.UpsertNodes) > 0 {
		backend = strings.ToLower(strings.TrimSpace(graph.UpsertNodes[0].TaskRef.BackendType))
	}
	if !matchesTaskGraphFilter(backend, rootID, filters) {
		return false
	}
	if len(filters.RepoScopes) > 0 {
		matched := false
		for _, scope := range filters.RepoScopes {
			scope = strings.ToLower(strings.TrimSpace(scope))
			if scope == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(graph.SourceContext.Repository), scope) {
				matched = true
				break
			}
			for _, node := range graph.UpsertNodes {
				if strings.EqualFold(strings.TrimSpace(node.SourceContext.Repository), scope) || strings.EqualFold(normalizeRepositoryScope(node.WorkspaceSpec), scope) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	if len(filters.Statuses) == 0 && len(filters.Labels) == 0 {
		return true
	}
	statusFilter := canonicalTaskStatusSet(filters.Statuses)
	labelFilter := canonicalNormalizedSet(filters.Labels)
	for _, node := range graph.UpsertNodes {
		task := contracts.Task{
			Status:   node.Status,
			Metadata: node.Metadata,
		}
		if matchesTaskGraphTaskFilters(task, statusFilter, labelFilter) {
			return true
		}
	}
	return false
}

func canonicalFilterValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		clean := strings.TrimSpace(value)
		if clean == "" {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func sanitizeTaskStatusMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range metadata {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		out[trimmedKey] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsString(values []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}

func generateTaskStatusCommandID(clock func() time.Time) string {
	if clock == nil {
		clock = time.Now
	}
	return clock().UTC().Format(time.RFC3339Nano) + "-" + "task-status"
}
