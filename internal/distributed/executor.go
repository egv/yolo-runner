package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type ExecutorWorkerOptions struct {
	ID                 string
	InstanceID         string
	Hostname           string
	Bus                Bus
	Runner             contracts.AgentRunner
	Backends           map[string]contracts.AgentRunner
	AgentResolver      func(map[string]string) (string, contracts.AgentRunner, error)
	Backend            string
	Subjects           EventSubjects
	Capabilities       []Capability
	SupportedPipelines []string
	SupportedAgents    []string
	DeclaredLanguages  []string
	DeclaredFeatures   []string
	EnvironmentProbes  ExecutorEnvironmentFeatureProbes
	CredentialFlags    map[string]bool
	ResourceHints      ExecutorResourceHints
	MaxConcurrency     int
	HeartbeatInterval  time.Duration
	RequestTimeout     time.Duration
	Clock              func() time.Time
	MaxRetries         int
	RetryDelay         time.Duration
	PreExecutionHook   func(context.Context, contracts.RunnerRequest, int)
	PostExecutionHook  func(context.Context, contracts.RunnerRequest, contracts.RunnerResult, error, int)
}

type ExecutorWorker struct {
	id                 string
	instanceID         string
	hostname           string
	bus                Bus
	runner             contracts.AgentRunner
	backends           map[string]contracts.AgentRunner
	agentResolver      func(map[string]string) (string, contracts.AgentRunner, error)
	defaultBackend     string
	subjects           EventSubjects
	capabilities       CapabilitySet
	supportedPipelines []string
	supportedAgents    []string
	declaredLanguages  []string
	declaredFeatures   []string
	environmentProbes  ExecutorEnvironmentFeatureProbes
	credentialFlags    map[string]bool
	resourceHints      ExecutorResourceHints
	maxConcurrency     int
	heartbeatInterval  time.Duration
	requestTimeout     time.Duration
	maxRetries         int
	retryDelay         time.Duration
	preExecutionHook   func(context.Context, contracts.RunnerRequest, int)
	postExecutionHook  func(context.Context, contracts.RunnerRequest, contracts.RunnerResult, error, int)
	clock              func() time.Time
	activeLoad         int64
}

func NewExecutorWorker(cfg ExecutorWorkerOptions) *ExecutorWorker {
	subjects := cfg.Subjects
	if subjects.Register == "" {
		subjects = DefaultEventSubjects("yolo")
	}
	if subjects.Offline == "" {
		subjects.Offline = subjects.Register
	}
	supportedPipelines := normalizeStringSet(cfg.SupportedPipelines)
	if len(supportedPipelines) == 0 {
		supportedPipelines = []string{"default"}
	}
	supportedAgents := normalizeStringSet(cfg.SupportedAgents)
	if len(supportedAgents) == 0 {
		supportedAgents = defaultSupportedAgents(cfg.Backends, cfg.Runner)
	}
	return &ExecutorWorker{
		id:                 strings.TrimSpace(cfg.ID),
		instanceID:         strings.TrimSpace(cfg.InstanceID),
		hostname:           strings.TrimSpace(cfg.Hostname),
		bus:                cfg.Bus,
		runner:             cfg.Runner,
		backends:           normalizeRunnerBackends(cfg.Backends),
		agentResolver:      cfg.AgentResolver,
		defaultBackend:     strings.TrimSpace(strings.ToLower(cfg.Backend)),
		subjects:           subjects,
		capabilities:       NewCapabilitySet(cfg.Capabilities...),
		supportedPipelines: supportedPipelines,
		supportedAgents:    supportedAgents,
		declaredLanguages:  normalizeStringSet(cfg.DeclaredLanguages),
		declaredFeatures:   normalizeStringSet(cfg.DeclaredFeatures),
		environmentProbes:  cfg.EnvironmentProbes,
		credentialFlags:    copyBoolMap(cfg.CredentialFlags),
		resourceHints:      cfg.ResourceHints,
		maxConcurrency:     cfg.MaxConcurrency,
		heartbeatInterval:  cfg.HeartbeatInterval,
		requestTimeout:     cfg.RequestTimeout,
		maxRetries:         cfg.MaxRetries,
		retryDelay:         cfg.RetryDelay,
		preExecutionHook:   cfg.PreExecutionHook,
		postExecutionHook:  cfg.PostExecutionHook,
		clock: func() time.Time {
			if cfg.Clock != nil {
				return cfg.Clock().UTC()
			}
			return time.Now().UTC()
		},
	}
}

func (w *ExecutorWorker) ID() string {
	if strings.TrimSpace(w.id) != "" {
		return strings.TrimSpace(w.id)
	}
	return "executor-" + w.clock().Format("20060102150405.000")
}

func (w *ExecutorWorker) Start(ctx context.Context) error {
	if w == nil || w.bus == nil {
		return fmt.Errorf("executor worker bus is required")
	}
	if w.runner == nil && len(w.backends) == 0 {
		return fmt.Errorf("executor worker runner is required")
	}
	interval := w.heartbeatInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if w.requestTimeout <= 0 {
		w.requestTimeout = 30 * time.Second
	}

	if err := w.publishRegistration(ctx); err != nil {
		return err
	}
	dispatchCh, unsubscribeDispatch, err := w.bus.Subscribe(ctx, w.subjects.TaskDispatch)
	if err != nil {
		return err
	}
	defer unsubscribeDispatch()

	heartbeatTicker := time.NewTicker(interval)
	defer heartbeatTicker.Stop()
	if err := w.publishHeartbeat(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			_ = w.publishOffline("shutdown")
			return ctx.Err()
		case <-heartbeatTicker.C:
			if err := w.publishHeartbeat(ctx); err != nil {
				return err
			}
		case raw, ok := <-dispatchCh:
			if !ok {
				return nil
			}
			go w.handleDispatch(ctx, raw)
		}
	}
}

func (w *ExecutorWorker) publishRegistration(ctx context.Context) error {
	registration := ExecutorRegistrationPayload{
		ExecutorID:              w.ID(),
		InstanceID:              w.instanceID,
		Hostname:                w.hostname,
		Capabilities:            keys(w.capabilities),
		SupportedPipelines:      append([]string(nil), w.supportedPipelines...),
		SupportedAgents:         append([]string(nil), w.supportedAgents...),
		DeclaredCapabilities:    ExecutorDeclaredCapabilities{Languages: append([]string(nil), w.declaredLanguages...), Features: append([]string(nil), w.declaredFeatures...)},
		EnvironmentProbes:       w.environmentProbes,
		CredentialFlags:         copyBoolMap(w.credentialFlags),
		ResourceHints:           w.resourceHints,
		MaxConcurrency:          w.maxConcurrency,
		CapabilitySchemaVersion: CapabilitySchemaVersionV1,
		StartedAt:               w.clock(),
	}
	event, err := NewEventEnvelope(EventTypeExecutorRegistered, w.ID(), "", registration)
	if err != nil {
		return err
	}
	return w.bus.Publish(ctx, w.subjects.Register, event)
}

func (w *ExecutorWorker) publishHeartbeat(ctx context.Context) error {
	currentLoad := int(atomic.LoadInt64(&w.activeLoad))
	maxConcurrency := w.maxConcurrency
	availableSlots := 0
	if maxConcurrency > 0 {
		availableSlots = maxConcurrency - currentLoad
		if availableSlots < 0 {
			availableSlots = 0
		}
	}
	heartbeat := ExecutorHeartbeatPayload{
		ExecutorID:     w.ID(),
		InstanceID:     w.instanceID,
		SeenAt:         w.clock(),
		CurrentLoad:    currentLoad,
		AvailableSlots: availableSlots,
		MaxConcurrency: maxConcurrency,
		HealthStatus:   "healthy",
	}
	event, err := NewEventEnvelope(EventTypeExecutorHeartbeat, w.ID(), "", heartbeat)
	if err != nil {
		return err
	}
	return w.bus.Publish(ctx, w.subjects.Heartbeat, event)
}

func (w *ExecutorWorker) handleDispatch(ctx context.Context, env EventEnvelope) {
	payload := TaskDispatchPayload{}
	if len(env.Payload) == 0 {
		return
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return
	}
	if strings.TrimSpace(payload.TargetExecutorID) != "" && payload.TargetExecutorID != w.ID() {
		return
	}
	if !w.capabilities.HasAll(payload.RequiredCapabilities...) {
		return
	}

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
	transport := runnerTransportRequest{}
	if len(payload.Request) == 0 {
		return
	}
	if err := json.Unmarshal(payload.Request, &transport); err != nil {
		return
	}
	request := contracts.RunnerRequest{
		TaskID:     transport.TaskID,
		ParentID:   transport.ParentID,
		Prompt:     transport.Prompt,
		Mode:       transport.Mode,
		Model:      transport.Model,
		RepoRoot:   transport.RepoRoot,
		Timeout:    transport.Timeout,
		MaxRetries: transport.MaxRetries,
		Metadata:   transport.Metadata,
	}
	atomic.AddInt64(&w.activeLoad, 1)
	defer atomic.AddInt64(&w.activeLoad, -1)

	maxRetries := request.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	if maxRetries == 0 && w.maxRetries > 0 {
		maxRetries = w.maxRetries
	}
	maxAttempts := maxRetries + 1
	requestCtx := ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}

	backend, requestRunner, err := w.resolveRunner(request.Metadata)
	if err != nil {
		response := TaskResultPayload{
			CorrelationID: payload.CorrelationID,
			ExecutorID:    w.ID(),
			Result: contracts.RunnerResult{
				Status: contracts.RunnerResultFailed,
				Reason: err.Error(),
			},
			Error: err.Error(),
		}
		responseEnv, envErr := NewEventEnvelope(EventTypeTaskResult, w.ID(), payload.CorrelationID, response)
		if envErr == nil {
			_ = w.bus.Publish(requestCtx, w.subjects.TaskResult, responseEnv)
		}
		return
	}
	if requestRunner == nil {
		finalErr := fmt.Errorf("no runner configured for backend %q", backend)
		response := TaskResultPayload{
			CorrelationID: payload.CorrelationID,
			ExecutorID:    w.ID(),
			Result: contracts.RunnerResult{
				Status: contracts.RunnerResultFailed,
				Reason: finalErr.Error(),
			},
			Error: finalErr.Error(),
		}
		responseEnv, envErr := NewEventEnvelope(EventTypeTaskResult, w.ID(), payload.CorrelationID, response)
		if envErr == nil {
			_ = w.bus.Publish(requestCtx, w.subjects.TaskResult, responseEnv)
		}
		return
	}
	executionTimeout := w.requestTimeoutFor(request)

	if request.Metadata == nil {
		request.Metadata = map[string]string{}
	}
	var finalResult contracts.RunnerResult
	var finalErr error
	w.emitRunnerEvent(requestCtx, request.TaskID, contracts.EventTypeRunnerStarted, map[string]string{
		"backend": backend,
		"phase":   "selection",
	})
attemptLoop:
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		w.emitRunnerEvent(requestCtx, request.TaskID, contracts.EventTypeRunnerStarted, map[string]string{
			"backend": backend,
			"attempt": strconv.Itoa(attempt),
		})
		if requestCtx.Err() != nil {
			finalErr = requestCtx.Err()
			break
		}
		if attempt > 1 && w.retryDelay > 0 {
			select {
			case <-time.After(w.retryDelay):
			case <-requestCtx.Done():
				finalErr = requestCtx.Err()
				break attemptLoop
			}
		}
		attemptRequest := cloneRunnerRequest(request)
		attemptRequest.OnProgress = w.progressForwarder(
			requestCtx,
			request.TaskID,
			attempt,
			backend,
			attemptRequest.OnProgress,
		)

		if w.preExecutionHook != nil {
			w.preExecutionHook(requestCtx, attemptRequest, attempt)
		}

		attemptCtx, cancel := contextForExecution(requestCtx, executionTimeout)
		result, err := requestRunner.Run(attemptCtx, attemptRequest)
		cancel()

		if err != nil {
			result.Status = contracts.RunnerResultFailed
			if strings.TrimSpace(result.Reason) == "" {
				result.Reason = err.Error()
			}
		}
		finalResult = result
		finalErr = err

		if w.postExecutionHook != nil {
			w.postExecutionHook(requestCtx, attemptRequest, result, err, attempt)
		}
		finishMetadata := map[string]string{
			"backend": backend,
			"attempt": strconv.Itoa(attempt),
			"status":  string(result.Status),
		}
		if strings.TrimSpace(result.Reason) != "" {
			finishMetadata["reason"] = result.Reason
		}
		if err != nil {
			finishMetadata["error"] = err.Error()
		}
		w.emitRunnerEvent(requestCtx, request.TaskID, contracts.EventTypeRunnerFinished, finishMetadata)
		if err == nil && result.Status == contracts.RunnerResultCompleted {
			break
		}
		if requestCtx.Err() != nil {
			finalErr = requestCtx.Err()
			break
		}
	}

	response := TaskResultPayload{
		CorrelationID: payload.CorrelationID,
		ExecutorID:    w.ID(),
		Result:        finalResult,
	}
	if finalErr != nil {
		response.Result = contracts.RunnerResult{
			Status: contracts.RunnerResultFailed,
			Reason: finalErr.Error(),
		}
		response.Error = finalErr.Error()
	}
	responseEnv, err := NewEventEnvelope(EventTypeTaskResult, w.ID(), payload.CorrelationID, response)
	if err != nil {
		return
	}
	_ = w.bus.Publish(requestCtx, w.subjects.TaskResult, responseEnv)
}

func (w *ExecutorWorker) publishOffline(reason string) error {
	payload := ExecutorOfflinePayload{
		ExecutorID: w.ID(),
		InstanceID: w.instanceID,
		SeenAt:     w.clock(),
		Reason:     strings.TrimSpace(reason),
	}
	event, err := NewEventEnvelope(EventTypeExecutorOffline, w.ID(), "", payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return w.bus.Publish(ctx, w.subjects.Offline, event)
}

func normalizeRunnerBackends(runners map[string]contracts.AgentRunner) map[string]contracts.AgentRunner {
	normalized := map[string]contracts.AgentRunner{}
	for backend, runner := range runners {
		key := strings.TrimSpace(strings.ToLower(backend))
		if key == "" || runner == nil {
			continue
		}
		normalized[key] = runner
	}
	return normalized
}

func (w *ExecutorWorker) resolveRunner(metadata map[string]string) (string, contracts.AgentRunner, error) {
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

func (w *ExecutorWorker) requestTimeoutFor(request contracts.RunnerRequest) time.Duration {
	if request.Timeout > 0 {
		return request.Timeout
	}
	return w.requestTimeout
}

func (w *ExecutorWorker) progressForwarder(ctx context.Context, taskID string, attempt int, backend string, original func(contracts.RunnerProgress)) func(contracts.RunnerProgress) {
	return func(progress contracts.RunnerProgress) {
		if w.bus == nil || w.subjects.MonitorEvent == "" {
			return
		}
		if original != nil {
			original(progress)
		}
		cloneProgressMetadata := cloneStringMap(progress.Metadata)
		if cloneProgressMetadata == nil {
			cloneProgressMetadata = map[string]string{}
		}
		cloneProgressMetadata["backend"] = backend
		cloneProgressMetadata["attempt"] = strconv.Itoa(attempt)
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now().UTC()
		}
		eventType := eventTypeForRunnerProgress(progress.Type)
		event := contracts.Event{
			Type:      eventType,
			TaskID:    taskID,
			WorkerID:  w.ID(),
			Message:   progress.Message,
			Metadata:  cloneProgressMetadata,
			Timestamp: eventTime,
		}
		progressEnv, envelopeErr := NewEventEnvelope(EventTypeMonitorEvent, w.ID(), "", MonitorEventPayload{Event: event})
		if envelopeErr != nil {
			return
		}
		_ = w.bus.Publish(ctx, w.subjects.MonitorEvent, progressEnv)
	}
}

func (w *ExecutorWorker) emitRunnerEvent(ctx context.Context, taskID string, eventType contracts.EventType, metadata map[string]string) {
	if w.bus == nil || w.subjects.MonitorEvent == "" {
		return
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata = cloneStringMap(metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if _, ok := metadata["backend"]; !ok {
		metadata["backend"] = w.defaultBackend
	}
	event := contracts.Event{
		Type:      eventType,
		TaskID:    taskID,
		WorkerID:  w.ID(),
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	}
	eventEnvelope, envelopeErr := NewEventEnvelope(EventTypeMonitorEvent, w.ID(), "", MonitorEventPayload{Event: event})
	if envelopeErr != nil {
		return
	}
	_ = w.bus.Publish(ctx, w.subjects.MonitorEvent, eventEnvelope)
}

func contextForExecution(parent context.Context, timeout time.Duration) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		return parent, func() {}
	}
	executionCtx, cancel := context.WithTimeout(parent, timeout)
	return executionCtx, cancel
}

func cloneRunnerRequest(request contracts.RunnerRequest) contracts.RunnerRequest {
	clone := request
	clone.Metadata = cloneStringMap(request.Metadata)
	if clone.Metadata == nil {
		clone.Metadata = map[string]string{}
	}
	clone.OnProgress = request.OnProgress
	return clone
}

func normalizeStringSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(strings.ToLower(value))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func copyBoolMap(values map[string]bool) map[string]bool {
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

func defaultSupportedAgents(backends map[string]contracts.AgentRunner, runner contracts.AgentRunner) []string {
	if len(backends) > 0 {
		names := make([]string, 0, len(backends))
		for name := range backends {
			names = append(names, name)
		}
		normalized := normalizeStringSet(names)
		if len(normalized) > 0 {
			return normalized
		}
	}
	if runner != nil {
		return []string{"default"}
	}
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[strings.TrimSpace(key)] = value
	}
	return out
}

func eventTypeForRunnerProgress(progressType string) contracts.EventType {
	switch strings.TrimSpace(progressType) {
	case "runner_cmd_started":
		return contracts.EventTypeRunnerCommandStarted
	case "runner_cmd_finished":
		return contracts.EventTypeRunnerCommandFinished
	case "runner_output":
		return contracts.EventTypeRunnerOutput
	case "runner_warning":
		return contracts.EventTypeRunnerWarning
	default:
		return contracts.EventTypeRunnerProgress
	}
}

func (w *ExecutorWorker) RequestService(ctx context.Context, request ServiceRequestPayload) (ServiceResponsePayload, error) {
	if w == nil || w.bus == nil {
		return ServiceResponsePayload{}, fmt.Errorf("executor worker not ready")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(request.RequestID) == "" {
		request.RequestID = strings.TrimSpace(request.TaskID) + "-" + strings.ReplaceAll(w.clock().Format(time.RFC3339Nano), ":", "")
	}
	request.ExecutorID = w.ID()
	if strings.TrimSpace(request.CorrelationID) == "" {
		request.CorrelationID = request.RequestID
	}

	responseCh, unsubscribeResponse, err := w.bus.Subscribe(ctx, w.subjects.ServiceResult)
	if err != nil {
		return ServiceResponsePayload{}, err
	}
	defer unsubscribeResponse()

	event, err := NewEventEnvelope(EventTypeServiceRequest, w.ID(), request.CorrelationID, request)
	if err != nil {
		return ServiceResponsePayload{}, err
	}
	if err := w.bus.Publish(ctx, w.subjects.ServiceRequest, event); err != nil {
		return ServiceResponsePayload{}, err
	}

	for {
		select {
		case raw, ok := <-responseCh:
			if !ok {
				return ServiceResponsePayload{}, fmt.Errorf("service response channel closed")
			}
			if raw.CorrelationID != request.CorrelationID {
				continue
			}
			response := ServiceResponsePayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &response); err != nil {
				continue
			}
			if response.RequestID != request.RequestID {
				continue
			}
			if response.Error != "" {
				return response, fmt.Errorf("%s", response.Error)
			}
			return response, nil
		case <-ctx.Done():
			return ServiceResponsePayload{}, ctx.Err()
		}
	}
}

func keys(values CapabilitySet) []Capability {
	out := make([]Capability, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}
