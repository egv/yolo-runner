package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type mastermindSchedulerOptions struct {
	queuePrefix      string
	backpressure     float64
	tickInterval     time.Duration
	statusAuthToken  string
	defaultQueueName string
	clock            func() time.Time
}

type mastermindScheduler struct {
	mu sync.Mutex

	queuePrefix      string
	backpressure     float64
	tickInterval     time.Duration
	statusAuthToken  string
	defaultQueueName string
	clock            func() time.Time

	trigger chan struct{}

	graphs     map[string]map[string]TaskGraphNode
	enqueued   map[string]struct{}
	inProgress map[string]struct{}
	blocked    map[string]struct{}

	roundRobin int
}

type schedulerEnqueueItem struct {
	key     string
	queue   string
	message QueueTaskMessage
}

type schedulerBlockedItem struct {
	key     string
	taskID  string
	backend string
	comment string
}

func newMastermindScheduler(cfg mastermindSchedulerOptions) *mastermindScheduler {
	queuePrefix := strings.TrimSpace(cfg.queuePrefix)
	if queuePrefix == "" {
		queuePrefix = strings.TrimSpace(cfg.defaultQueueName)
	}
	if queuePrefix == "" {
		queuePrefix = "queue.tasks"
	}
	backpressure := cfg.backpressure
	if backpressure <= 0 {
		backpressure = 1
	}
	clock := cfg.clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &mastermindScheduler{
		queuePrefix:      queuePrefix,
		backpressure:     backpressure,
		tickInterval:     cfg.tickInterval,
		statusAuthToken:  strings.TrimSpace(cfg.statusAuthToken),
		defaultQueueName: strings.TrimSpace(cfg.defaultQueueName),
		clock:            clock,
		trigger:          make(chan struct{}, 1),
		graphs:           map[string]map[string]TaskGraphNode{},
		enqueued:         map[string]struct{}{},
		inProgress:       map[string]struct{}{},
		blocked:          map[string]struct{}{},
	}
}

func (m *Mastermind) schedulerApplySnapshot(payload TaskGraphSnapshotPayload) {
	if m == nil || m.scheduler == nil {
		return
	}
	graphs, err := payload.NormalizeGraphs()
	if err != nil {
		return
	}
	m.scheduler.mu.Lock()
	defer m.scheduler.mu.Unlock()
	for _, graph := range graphs {
		graphRef := strings.TrimSpace(graph.GraphRef)
		if graphRef == "" {
			continue
		}
		nodes := make(map[string]TaskGraphNode, len(graph.Nodes))
		for _, node := range graph.Nodes {
			node.TaskID = strings.TrimSpace(node.TaskID)
			if node.TaskID == "" {
				continue
			}
			if strings.TrimSpace(node.GraphRef) == "" {
				node.GraphRef = graphRef
			}
			nodes[node.TaskID] = node
		}
		m.scheduler.graphs[graphRef] = nodes
	}
	m.scheduler.pruneStateLocked()
}

func (m *Mastermind) schedulerApplyDiff(payload TaskGraphDiffPayload) {
	if m == nil || m.scheduler == nil {
		return
	}
	m.scheduler.mu.Lock()
	defer m.scheduler.mu.Unlock()
	for _, graph := range payload.Graphs {
		graphRef := strings.TrimSpace(graph.GraphRef)
		if graphRef == "" {
			continue
		}
		nodes, ok := m.scheduler.graphs[graphRef]
		if !ok {
			nodes = map[string]TaskGraphNode{}
			m.scheduler.graphs[graphRef] = nodes
		}
		for _, node := range graph.UpsertNodes {
			node.TaskID = strings.TrimSpace(node.TaskID)
			if node.TaskID == "" {
				continue
			}
			if strings.TrimSpace(node.GraphRef) == "" {
				node.GraphRef = graphRef
			}
			nodes[node.TaskID] = node
		}
		for _, taskID := range graph.DeleteTaskIDs {
			taskID = strings.TrimSpace(taskID)
			if taskID == "" {
				continue
			}
			delete(nodes, taskID)
			key := schedulerTaskKey(graphRef, taskID)
			delete(m.scheduler.enqueued, key)
			delete(m.scheduler.inProgress, key)
			delete(m.scheduler.blocked, key)
		}
	}
	m.scheduler.pruneStateLocked()
}

func (m *Mastermind) schedulerApplyMonitorEvent(event contracts.Event) bool {
	if m == nil || m.scheduler == nil {
		return false
	}
	taskID := strings.TrimSpace(event.TaskID)
	if taskID == "" {
		return false
	}
	graphRef := strings.TrimSpace(event.Metadata["graph_ref"])
	if graphRef == "" {
		graphRef = m.scheduler.findGraphRef(taskID)
	}
	if graphRef == "" {
		return false
	}
	key := schedulerTaskKey(graphRef, taskID)
	changed := false

	m.scheduler.mu.Lock()
	switch event.Type {
	case contracts.EventTypeTaskStarted:
		delete(m.scheduler.enqueued, key)
		if _, exists := m.scheduler.inProgress[key]; !exists {
			m.scheduler.inProgress[key] = struct{}{}
			changed = true
		}
		if nodes, ok := m.scheduler.graphs[graphRef]; ok {
			if node, exists := nodes[taskID]; exists {
				node.Status = contracts.TaskStatusInProgress
				nodes[taskID] = node
			}
		}
	case contracts.EventTypeTaskCompleted:
		delete(m.scheduler.enqueued, key)
		delete(m.scheduler.inProgress, key)
		delete(m.scheduler.blocked, key)
		changed = true
		if nodes, ok := m.scheduler.graphs[graphRef]; ok {
			if node, exists := nodes[taskID]; exists {
				node.Status = contracts.TaskStatusClosed
				nodes[taskID] = node
			}
		}
	case contracts.EventTypeTaskFailed:
		delete(m.scheduler.enqueued, key)
		delete(m.scheduler.inProgress, key)
		changed = true
		if nodes, ok := m.scheduler.graphs[graphRef]; ok {
			if node, exists := nodes[taskID]; exists {
				node.Status = contracts.TaskStatusBlocked
				nodes[taskID] = node
			}
		}
	}
	m.scheduler.mu.Unlock()

	if !changed {
		return false
	}

	backend := strings.ToLower(strings.TrimSpace(event.Metadata["task_backend"]))
	if backend == "" {
		backend = m.scheduler.backendFor(graphRef, taskID)
	}
	comment := strings.TrimSpace(event.Message)
	switch event.Type {
	case contracts.EventTypeTaskStarted:
		if comment == "" {
			comment = "execution started"
		}
		_ = m.schedulerPublishStatus(context.Background(), taskID, contracts.TaskStatusInProgress, backend, comment)
	case contracts.EventTypeTaskCompleted:
		if comment == "" {
			comment = "execution completed"
		}
		_ = m.schedulerPublishStatus(context.Background(), taskID, contracts.TaskStatusClosed, backend, comment)
	case contracts.EventTypeTaskFailed:
		if comment == "" {
			comment = "execution failed"
		}
		_ = m.schedulerPublishStatus(context.Background(), taskID, contracts.TaskStatusBlocked, backend, comment)
	}
	return true
}

func (m *Mastermind) startSchedulerLoop(ctx context.Context) {
	if m == nil || m.scheduler == nil || m.bus == nil {
		return
	}
	interval := m.scheduler.tickInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.scheduleOnce(ctx)
		case <-m.scheduler.trigger:
			m.scheduleOnce(ctx)
		}
	}
}

func (m *Mastermind) triggerScheduler(_ context.Context) {
	if m == nil || m.scheduler == nil {
		return
	}
	select {
	case m.scheduler.trigger <- struct{}{}:
	default:
	}
}

func (m *Mastermind) scheduleOnce(ctx context.Context) {
	if m == nil || m.scheduler == nil || m.bus == nil {
		return
	}
	executors := m.registry.Snapshot()
	enqueueItems, blockedItems := m.scheduler.plan(executors)
	for _, blocked := range blockedItems {
		if err := m.schedulerPublishStatus(ctx, blocked.taskID, contracts.TaskStatusBlocked, blocked.backend, blocked.comment); err == nil {
			m.scheduler.markBlocked(blocked.key)
		}
	}
	for _, item := range enqueueItems {
		env, err := NewEventEnvelope(EventTypeTaskDispatch, m.id, item.key, item.message)
		if err != nil {
			continue
		}
		env.CorrelationID = item.key
		env.IdempotencyKey = item.key
		if err := m.bus.Enqueue(ctx, item.queue, env); err != nil {
			continue
		}
		m.scheduler.markEnqueued(item.key)
	}
}

func (m *Mastermind) schedulerPublishStatus(ctx context.Context, taskID string, status contracts.TaskStatus, backend string, comment string) error {
	if m == nil {
		return fmt.Errorf("mastermind is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := TaskStatusUpdatePayload{
		TaskID:    strings.TrimSpace(taskID),
		Status:    status,
		Comment:   strings.TrimSpace(comment),
		AuthToken: strings.TrimSpace(m.statusUpdateAuthToken),
	}
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend != "" {
		request.Backends = []string{backend}
	}
	_, err := m.PublishTaskStatusUpdate(ctx, request)
	return err
}

func (s *mastermindScheduler) plan(executors []ExecutorAdvertisement) ([]schedulerEnqueueItem, []schedulerBlockedItem) {
	s.mu.Lock()
	defer s.mu.Unlock()

	freeSlots := 0
	for _, executor := range executors {
		if executor.AvailableSlots > 0 {
			freeSlots += executor.AvailableSlots
		}
	}
	if freeSlots < 0 {
		freeSlots = 0
	}
	maxInFlight := int(float64(freeSlots) * s.backpressure)
	if maxInFlight < 0 {
		maxInFlight = 0
	}
	inFlight := len(s.enqueued) + len(s.inProgress)
	budget := maxInFlight - inFlight
	if budget < 0 {
		budget = 0
	}

	runnable := s.nextRunnableLocked()
	enqueueItems := make([]schedulerEnqueueItem, 0)
	blockedItems := make([]schedulerBlockedItem, 0)
	for _, node := range runnable {
		key := schedulerTaskKey(node.GraphRef, node.TaskID)
		if _, exists := s.enqueued[key]; exists {
			continue
		}
		if _, exists := s.inProgress[key]; exists {
			continue
		}

		requirements := deriveQueueTaskRequirements(node)
		matches := anyExecutorMatches(requirements, executors)
		if matches {
			delete(s.blocked, key)
		}
		if !matches {
			if _, alreadyBlocked := s.blocked[key]; alreadyBlocked {
				continue
			}
			blockedItems = append(blockedItems, schedulerBlockedItem{
				key:     key,
				taskID:  schedulerTaskNativeID(node),
				backend: schedulerTaskBackend(node),
				comment: "blocked: no executor matches required capabilities/environment/credentials",
			})
			continue
		}
		if budget <= 0 {
			continue
		}
		request, err := schedulerRunnerRequest(node, requirements)
		if err != nil {
			continue
		}
		workspace := deriveQueueWorkspaceSpec(node)
		enqueueItems = append(enqueueItems, schedulerEnqueueItem{
			key:   key,
			queue: routeQueueName(s.queuePrefix, requirements),
			message: QueueTaskMessage{
				TaskRef: QueueTaskRef{
					Backend:  schedulerTaskBackend(node),
					NativeID: schedulerTaskNativeID(node),
				},
				WorkspaceSpec: workspace,
				Requirements:  requirements,
				GraphRef:      strings.TrimSpace(node.GraphRef),
				Request:       request,
			},
		})
		budget--
	}
	return enqueueItems, blockedItems
}

func (s *mastermindScheduler) markEnqueued(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueued[strings.TrimSpace(key)] = struct{}{}
}

func (s *mastermindScheduler) markBlocked(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[strings.TrimSpace(key)] = struct{}{}
}

func (s *mastermindScheduler) findGraphRef(taskID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	graphRefs := make([]string, 0, len(s.graphs))
	for graphRef := range s.graphs {
		graphRefs = append(graphRefs, graphRef)
	}
	sort.Strings(graphRefs)
	for _, graphRef := range graphRefs {
		if _, ok := s.graphs[graphRef][taskID]; ok {
			return graphRef
		}
	}
	return ""
}

func (s *mastermindScheduler) backendFor(graphRef string, taskID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	nodes, ok := s.graphs[strings.TrimSpace(graphRef)]
	if !ok {
		return ""
	}
	node, ok := nodes[strings.TrimSpace(taskID)]
	if !ok {
		return ""
	}
	return schedulerTaskBackend(node)
}

func (s *mastermindScheduler) pruneStateLocked() {
	valid := map[string]struct{}{}
	for graphRef, nodes := range s.graphs {
		for taskID, node := range nodes {
			key := schedulerTaskKey(graphRef, taskID)
			valid[key] = struct{}{}
			if node.Status != contracts.TaskStatusOpen && node.Status != contracts.TaskStatusInProgress {
				delete(s.enqueued, key)
				delete(s.inProgress, key)
				delete(s.blocked, key)
			}
		}
	}
	for key := range s.enqueued {
		if _, ok := valid[key]; !ok {
			delete(s.enqueued, key)
		}
	}
	for key := range s.inProgress {
		if _, ok := valid[key]; !ok {
			delete(s.inProgress, key)
		}
	}
	for key := range s.blocked {
		if _, ok := valid[key]; !ok {
			delete(s.blocked, key)
		}
	}
}

func (s *mastermindScheduler) nextRunnableLocked() []TaskGraphNode {
	graphRefs := make([]string, 0, len(s.graphs))
	for graphRef := range s.graphs {
		graphRefs = append(graphRefs, graphRef)
	}
	sort.Strings(graphRefs)
	if len(graphRefs) == 0 {
		return nil
	}
	start := 0
	if len(graphRefs) > 0 {
		start = s.roundRobin % len(graphRefs)
	}
	ordered := append([]string{}, graphRefs[start:]...)
	ordered = append(ordered, graphRefs[:start]...)
	s.roundRobin++

	remaining := make(map[string][]TaskGraphNode, len(ordered))
	for _, graphRef := range ordered {
		remaining[graphRef] = runnableCandidates(graphRef, s.graphs[graphRef])
	}

	result := make([]TaskGraphNode, 0)
	for {
		picked := 0
		for _, graphRef := range ordered {
			candidates := remaining[graphRef]
			if len(candidates) == 0 {
				continue
			}
			next := candidates[0]
			remaining[graphRef] = candidates[1:]
			result = append(result, next)
			picked++
		}
		if picked == 0 {
			break
		}
	}
	return result
}

func runnableCandidates(graphRef string, nodes map[string]TaskGraphNode) []TaskGraphNode {
	candidates := make([]TaskGraphNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Status != contracts.TaskStatusOpen {
			continue
		}
		if strings.TrimSpace(node.TaskID) == strings.TrimSpace(graphRef) && hasChildren(nodes, node.TaskID) {
			continue
		}
		if !dependenciesSatisfied(node, nodes) {
			continue
		}
		candidates = append(candidates, node)
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftPriority := taskPriorityFromMetadata(candidates[i].Metadata)
		rightPriority := taskPriorityFromMetadata(candidates[j].Metadata)
		if leftPriority == rightPriority {
			return strings.TrimSpace(candidates[i].TaskID) < strings.TrimSpace(candidates[j].TaskID)
		}
		return leftPriority > rightPriority
	})
	return candidates
}

func taskPriorityFromMetadata(metadata map[string]string) int {
	if len(metadata) == 0 {
		return 0
	}
	raw := strings.TrimSpace(metadata["priority"])
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func hasChildren(nodes map[string]TaskGraphNode, taskID string) bool {
	taskID = strings.TrimSpace(taskID)
	for _, node := range nodes {
		if strings.TrimSpace(node.ParentTaskID) == taskID {
			return true
		}
	}
	return false
}

func dependenciesSatisfied(node TaskGraphNode, nodes map[string]TaskGraphNode) bool {
	deps := make([]string, 0, 4)
	if parent := strings.TrimSpace(node.ParentTaskID); parent != "" {
		deps = append(deps, parent)
	}
	deps = append(deps, parseTaskDependencyMetadata(node.Metadata)...)
	seen := map[string]struct{}{}
	for _, depID := range deps {
		depID = strings.TrimSpace(depID)
		if depID == "" {
			continue
		}
		if _, ok := seen[depID]; ok {
			continue
		}
		seen[depID] = struct{}{}
		depNode, exists := nodes[depID]
		if !exists {
			return false
		}
		if depNode.Status != contracts.TaskStatusClosed {
			return false
		}
	}
	return true
}

func parseTaskDependencyMetadata(metadata map[string]string) []string {
	if len(metadata) == 0 {
		return nil
	}
	keys := []string{"depends_on", "depends-on", "blocked_by", "blocked-by", "deps"}
	deps := make([]string, 0)
	for _, key := range keys {
		raw := strings.TrimSpace(metadata[key])
		if raw == "" {
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			deps = append(deps, trimmed)
		}
	}
	return deps
}

func deriveQueueTaskRequirements(node TaskGraphNode) QueueTaskRequirements {
	req := QueueTaskRequirements{Metadata: map[string]bool{}}
	for _, requirement := range node.Requirements {
		name := strings.TrimSpace(requirement.Name)
		kind := strings.ToLower(strings.TrimSpace(requirement.Kind))
		detail := strings.TrimSpace(requirement.Detail)
		switch kind {
		case "capability":
			if capability, ok := parseCapabilityRequirement(name, detail); ok {
				req.Capabilities = append(req.Capabilities, capability)
			}
		case "credential_flag":
			if name != "" {
				req.Credentials = append(req.Credentials, name)
			}
		case "environment", "environment_feature":
			if name != "" {
				req.Metadata[name] = true
			}
		default:
			if strings.HasPrefix(strings.ToLower(name), "has_env:") {
				req.Credentials = append(req.Credentials, name)
				continue
			}
			if capability, ok := parseCapabilityRequirement(name, detail); ok {
				req.Capabilities = append(req.Capabilities, capability)
			}
		}
	}
	if len(req.Capabilities) == 0 {
		req.Capabilities = []Capability{CapabilityImplement}
	}
	req.Capabilities = uniqueCapabilities(req.Capabilities)
	req.Credentials = uniqueStrings(req.Credentials)
	if len(req.Metadata) == 0 {
		req.Metadata = nil
	}
	return req
}

func parseCapabilityRequirement(name string, detail string) (Capability, bool) {
	for _, candidate := range []string{name, detail} {
		normalized := strings.ToLower(strings.TrimSpace(candidate))
		switch Capability(normalized) {
		case CapabilityImplement, CapabilityReview, CapabilityRewriteTask, CapabilityLargerModel, CapabilityServiceProxy:
			return Capability(normalized), true
		}
	}
	return "", false
}

func uniqueCapabilities(values []Capability) []Capability {
	seen := map[Capability]struct{}{}
	out := make([]Capability, 0, len(values))
	for _, value := range values {
		normalized := Capability(strings.ToLower(strings.TrimSpace(string(value))))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func schedulerRunnerRequest(node TaskGraphNode, requirements QueueTaskRequirements) (json.RawMessage, error) {
	mode := contracts.RunnerModeImplement
	for _, capability := range requirements.Capabilities {
		if capability == CapabilityReview {
			mode = contracts.RunnerModeReview
			break
		}
	}
	metadata := sanitizeTaskStatusMetadata(node.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	backend := schedulerTaskBackend(node)
	if backend != "" {
		metadata["backend"] = backend
	}
	request := contracts.RunnerRequest{
		TaskID:   schedulerTaskNativeID(node),
		Prompt:   strings.TrimSpace(node.Title),
		Mode:     mode,
		Metadata: metadata,
	}
	return requestForTransport(request)
}

func deriveQueueWorkspaceSpec(node TaskGraphNode) QueueWorkspaceSpec {
	if node.WorkspaceSpec != nil {
		kind := strings.ToLower(strings.TrimSpace(node.WorkspaceSpec.Kind))
		switch kind {
		case QueueWorkspaceGit:
			if repoURL := strings.TrimSpace(node.WorkspaceSpec.RepoURL); repoURL != "" {
				return QueueWorkspaceSpec{Kind: QueueWorkspaceGit, Git: &QueueGitWorkspaceSpec{RepoURL: repoURL, BaseRef: strings.TrimSpace(node.WorkspaceSpec.Ref)}}
			}
		case QueueWorkspaceNone:
			return QueueWorkspaceSpec{Kind: QueueWorkspaceNone}
		}
	}
	provider := strings.ToLower(strings.TrimSpace(node.SourceContext.Provider))
	repo := strings.TrimSpace(node.SourceContext.Repository)
	if provider == "github" && repo != "" {
		return QueueWorkspaceSpec{Kind: QueueWorkspaceGit, Git: &QueueGitWorkspaceSpec{RepoURL: "https://github.com/" + strings.TrimPrefix(repo, "https://github.com/")}}
	}
	return QueueWorkspaceSpec{Kind: QueueWorkspaceNone}
}

func routeQueueName(prefix string, requirements QueueTaskRequirements) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "queue.tasks"
	}
	if len(requirements.Capabilities) == 0 {
		return prefix
	}
	return prefix + "." + strings.ToLower(strings.TrimSpace(string(requirements.Capabilities[0])))
}

func anyExecutorMatches(requirements QueueTaskRequirements, executors []ExecutorAdvertisement) bool {
	for _, executor := range executors {
		if executorMatches(requirements, executor) {
			return true
		}
	}
	return false
}

func executorMatches(requirements QueueTaskRequirements, executor ExecutorAdvertisement) bool {
	if len(requirements.Capabilities) > 0 {
		if len(executor.Capabilities) == 0 {
			return false
		}
		if !executor.Capabilities.HasAll(requirements.Capabilities...) {
			return false
		}
	}
	for _, credential := range requirements.Credentials {
		if !executor.CredentialFlags[strings.TrimSpace(credential)] {
			return false
		}
	}
	for key, required := range requirements.Metadata {
		if !required {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(key))
		switch normalized {
		case "docker", "has_docker":
			if !executor.EnvironmentProbes.HasDocker {
				return false
			}
		case "git", "has_git":
			if !executor.EnvironmentProbes.HasGit {
				return false
			}
		case "go", "has_go":
			if !executor.EnvironmentProbes.HasGo {
				return false
			}
		}
	}
	return true
}

func schedulerTaskKey(graphRef string, taskID string) string {
	return strings.TrimSpace(graphRef) + "|" + strings.TrimSpace(taskID)
}

func schedulerTaskNativeID(node TaskGraphNode) string {
	nativeID := strings.TrimSpace(node.TaskRef.BackendNativeID)
	if nativeID != "" {
		return nativeID
	}
	return strings.TrimSpace(node.TaskID)
}

func schedulerTaskBackend(node TaskGraphNode) string {
	backend := strings.ToLower(strings.TrimSpace(node.TaskRef.BackendType))
	if backend != "" {
		return backend
	}
	return strings.ToLower(strings.TrimSpace(node.SourceContext.Provider))
}
