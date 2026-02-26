package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
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

type MastermindOptions struct {
	ID                    string
	Bus                   Bus
	Subjects              EventSubjects
	RegistryTTL           time.Duration
	RequestTimeout        time.Duration
	Clock                 func() time.Time
	ServiceHandler        ServiceHandler
	StatusUpdateBackends  map[string]TaskStatusWriter
	StatusUpdateAuthToken string
	TaskGraphSyncRoots    []string
	TaskGraphSyncInterval time.Duration
}

type TaskDispatchRequest struct {
	RunnerRequest        contracts.RunnerRequest
	RequiredCapabilities []Capability
}

type Mastermind struct {
	id                    string
	bus                   Bus
	subjects              EventSubjects
	registry              *ExecutorRegistry
	requestTimeout        time.Duration
	clock                 func() time.Time
	serviceHandler        ServiceHandler
	statusUpdateBackends  map[string]TaskStatusWriter
	statusUpdateAuthToken string
	taskGraphSyncRoots    []string
	taskGraphSyncInterval time.Duration
	taskStatusMu          sync.RWMutex
	taskStatusVersions    map[string]map[string]int64
	taskGraphs            map[string]TaskGraphSnapshotPayload
	taskGraphsMu          sync.RWMutex
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
		serviceHandler:        cfg.ServiceHandler,
		statusUpdateBackends:  statusUpdateBackends,
		statusUpdateAuthToken: strings.TrimSpace(cfg.StatusUpdateAuthToken),
		taskGraphSyncRoots:    canonicalFilterValues(cfg.TaskGraphSyncRoots),
		taskGraphSyncInterval: cfg.TaskGraphSyncInterval,
		taskStatusVersions:    make(map[string]map[string]int64),
		taskGraphs:            map[string]TaskGraphSnapshotPayload{},
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

	go m.startTaskGraphSync(ctx)

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
	executor, err := m.registry.Pick(capabilities...)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	correlationID := req.RunnerRequest.TaskID + "-" + strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "")
	dispatchTimeout := m.requestTimeout
	if dispatchTimeout <= 0 {
		dispatchTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()

	resultCh, unsubResult, err := m.bus.Subscribe(ctx, m.subjects.TaskResult)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer unsubResult()

	dispatch := TaskDispatchPayload{
		CorrelationID:        correlationID,
		TaskID:               req.RunnerRequest.TaskID,
		TargetExecutorID:     executor.ID,
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

	timeoutTicker := time.NewTicker(50 * time.Millisecond)
	defer timeoutTicker.Stop()
	for {
		select {
		case raw := <-resultCh:
			if raw.CorrelationID != correlationID {
				continue
			}
			payload := TaskResultPayload{}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.CorrelationID) != correlationID {
				continue
			}
			if strings.TrimSpace(payload.ExecutorID) == "" {
				payload.ExecutorID = strings.TrimSpace(payload.ExecutorID)
			}
			if payload.Error != "" {
				return contracts.RunnerResult{}, fmt.Errorf("executor failed: %s", payload.Error)
			}
			return payload.Result, nil
		case <-timeoutTicker.C:
			if !m.registry.IsAvailable(executor.ID, m.clock()) {
				return contracts.RunnerResult{}, fmt.Errorf("executor %s disconnected", executor.ID)
			}
		case <-ctx.Done():
			return contracts.RunnerResult{}, fmt.Errorf("task dispatch timed out: %w", ctx.Err())
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
	if len(m.taskGraphSyncRoots) == 0 || len(m.taskGraphSyncBackends()) == 0 {
		return
	}

	interval := m.taskGraphSyncInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	previousSignatures := map[string]string{}
	previousTrees := map[string]contracts.TaskTree{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	syncTaskGraphs := func() {
		for _, backendID := range m.taskGraphSyncBackends() {
			backend := m.statusUpdateBackends[backendID]
			if backend == nil {
				continue
			}
			for _, rootID := range m.taskGraphSyncRoots {
				rootID = strings.TrimSpace(rootID)
				if rootID == "" {
					continue
				}
				tree, err := backend.GetTaskTree(ctx, rootID)
				if err != nil || tree == nil {
					continue
				}
				snapshot := TaskGraphSnapshotPayload{
					Backend:  backendID,
					RootID:   rootID,
					TaskTree: *tree,
					Metadata: map[string]string{
						"published_by": m.id,
						"published_at": m.clock().Format(time.RFC3339Nano),
					},
				}
				snapshotEnv, err := NewEventEnvelope(EventTypeTaskGraphSnapshot, m.id, rootID, snapshot)
				if err == nil {
					_ = m.bus.Publish(ctx, m.subjects.TaskGraphSnapshot, snapshotEnv)
				}

				cacheKey := taskGraphCacheKey(backendID, rootID)
				currentSig := buildTaskTreeSignature(&snapshot.TaskTree)
				previousSig, hasPrevious := previousSignatures[cacheKey]
				if hasPrevious && currentSig != "" && currentSig != previousSig {
					previousTree := previousTrees[cacheKey]
					changes := buildTaskGraphDiffChanges(previousTree, *tree)
					if len(changes) == 0 {
						changes = []string{"task-graph-updated"}
					}
					diff := TaskGraphDiffPayload{
						Backend: backendID,
						RootID:  rootID,
						Changes: changes,
						Metadata: map[string]string{
							"published_by": m.id,
							"published_at": m.clock().Format(time.RFC3339Nano),
						},
					}
					diffEnv, err := NewEventEnvelope(EventTypeTaskGraphDiff, m.id, rootID, diff)
					if err == nil {
						_ = m.bus.Publish(ctx, m.subjects.TaskGraphDiff, diffEnv)
					}
				}
				previousSignatures[cacheKey] = currentSig
				previousTrees[cacheKey] = *cloneTaskTree(tree)
			}
		}
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

func (m *Mastermind) taskGraphSyncBackends() []string {
	if m == nil {
		return nil
	}
	backendIDs := make([]string, 0, len(m.statusUpdateBackends))
	for backendID, backend := range m.statusUpdateBackends {
		if backend == nil {
			continue
		}
		backendID = strings.ToLower(strings.TrimSpace(backendID))
		if backendID == "" {
			continue
		}
		backendIDs = append(backendIDs, backendID)
	}
	sort.Strings(backendIDs)
	return backendIDs
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

	closeOnce := sync.Once{}
	unsubscribe := func() {
		closeOnce.Do(func() {
			unsubscribeSnapshot()
			unsubscribeDiff()
			close(output)
		})
	}

	go func() {
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
				if !matchesTaskGraphFilter(payload.Backend, payload.RootID, filters) {
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
				if !matchesTaskGraphFilter(payload.Backend, payload.RootID, filters) {
					continue
				}
				event := TaskGraphEvent{
					Type: EventTypeTaskGraphDiff,
					Diff: &payload,
				}
				select {
				case output <- event:
				default:
				}
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

func (m *Mastermind) AckStatusUpdate(ctx context.Context, payload TaskStatusUpdateAckPayload) error {
	if m == nil || m.bus == nil {
		return fmt.Errorf("mastermind bus is required")
	}
	payload.CommandID = strings.TrimSpace(payload.CommandID)
	payload.TaskID = strings.TrimSpace(payload.TaskID)
	payload.Backends = canonicalTaskStatusUpdatesBackends(payload.Backends)
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

	if request.TaskID == "" {
		return fmt.Errorf("task id is required for task status update")
	}
	if request.Status == "" {
		return m.rejectStatusUpdate(ctx, request, "missing status")
	}
	if m.statusUpdateAuthToken != "" && request.AuthToken != m.statusUpdateAuthToken {
		return m.rejectStatusUpdate(ctx, request, "missing or invalid auth token")
	}
	if request.CommandID == "" {
		request.CommandID = env.CorrelationID
		if request.CommandID == "" {
			request.CommandID = generateTaskStatusCommandID(m.clock)
		}
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

	return m.AckStatusUpdate(ctx, TaskStatusUpdateAckPayload{
		TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
			CommandID: request.CommandID,
			TaskID:    request.TaskID,
			Status:    request.Status,
			Backends:  backends,
			Versions:  newVersions,
			Result:    "ok",
		},
	})
}

func (m *Mastermind) rejectStatusUpdate(_ context.Context, request TaskStatusUpdatePayload, reason string) error {
	if m == nil {
		return fmt.Errorf("mastermind unavailable: %s", reason)
	}
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
