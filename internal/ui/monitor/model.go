package monitor

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type Model struct {
	now          func() time.Time
	root         RunState
	runStartedAt time.Time
	eventCount   int
	runParams    map[string]string
	currentTask  string
	currentTitle string
	phase        string
	lastOutputAt time.Time
	history      []string
	workers      map[string]workerLane
	landing      map[string]landingState
	triage       map[string]triageState
}

type Snapshot struct {
	Root RunState
}

type RunState struct {
	RunID   string
	Workers map[string]WorkerState
	Tasks   map[string]TaskState
}

type WorkerState struct {
	WorkerID        string
	CurrentTaskID   string
	CurrentTask     string
	CurrentPhase    string
	CurrentQueuePos int
}

type TaskState struct {
	TaskID               string
	Title                string
	WorkerID             string
	QueuePos             int
	RunnerPhase          string
	LastMessage          string
	LastUpdateAt         time.Time
	CommandStartedCount  int
	CommandFinishedCount int
	OutputCount          int
	WarningCount         int
	WarningActive        bool
	TerminalStatus       string
	LastCommandStarted   string
	LastCommandSummary   string
	LastSeverity         string
}

type workerLane struct {
	taskID    string
	taskTitle string
	phase     string
	queuePos  int
}

type landingState struct {
	taskID    string
	taskTitle string
	status    string
}

type triageState struct {
	taskID    string
	taskTitle string
	status    string
	reason    string
}

func NewModel(now func() time.Time) *Model {
	if now == nil {
		now = time.Now
	}
	return &Model{
		now:       now,
		root:      RunState{Workers: map[string]WorkerState{}, Tasks: map[string]TaskState{}},
		runParams: map[string]string{},
		history:   []string{},
		workers:   map[string]workerLane{},
		landing:   map[string]landingState{},
		triage:    map[string]triageState{},
	}
}

func (m *Model) Apply(event contracts.Event) {
	m.eventCount++
	if event.TaskID != "" {
		m.currentTask = event.TaskID
	}
	if event.TaskTitle != "" {
		m.currentTitle = event.TaskTitle
	}
	if event.Type != "" {
		m.phase = string(event.Type)
	}
	if !event.Timestamp.IsZero() {
		m.lastOutputAt = event.Timestamp
	} else {
		m.lastOutputAt = m.now()
	}
	if workerID := strings.TrimSpace(event.WorkerID); workerID != "" {
		worker := m.root.Workers[workerID]
		worker.WorkerID = workerID
		worker.CurrentTaskID = event.TaskID
		if title := strings.TrimSpace(event.TaskTitle); title != "" {
			worker.CurrentTask = title
		}
		worker.CurrentPhase = string(event.Type)
		if event.QueuePos > 0 {
			worker.CurrentQueuePos = event.QueuePos
		}
		m.root.Workers[workerID] = worker
	}

	if strings.TrimSpace(event.TaskID) != "" {
		task := m.root.Tasks[event.TaskID]
		task.TaskID = event.TaskID
		if title := strings.TrimSpace(event.TaskTitle); title != "" {
			task.Title = title
		}
		if workerID := strings.TrimSpace(event.WorkerID); workerID != "" {
			task.WorkerID = workerID
		}
		if event.QueuePos > 0 {
			task.QueuePos = event.QueuePos
		}
		task.RunnerPhase = string(event.Type)
		if message := strings.TrimSpace(event.Message); message != "" {
			task.LastMessage = message
		}
		task.LastUpdateAt = event.Timestamp
		applyDerivedTaskEvent(&task, event)
		m.root.Tasks[event.TaskID] = task
	}

	if workerID := strings.TrimSpace(event.WorkerID); workerID != "" {
		lane := m.workers[workerID]
		lane.taskID = event.TaskID
		if title := strings.TrimSpace(event.TaskTitle); title != "" {
			lane.taskTitle = title
		}
		lane.phase = string(event.Type)
		if event.QueuePos > 0 {
			lane.queuePos = event.QueuePos
		}
		m.workers[workerID] = lane
	}
	if event.Type == contracts.EventTypeTaskFinished {
		taskID := strings.TrimSpace(event.TaskID)
		if taskID != "" {
			m.landing[taskID] = landingState{
				taskID:    taskID,
				taskTitle: strings.TrimSpace(event.TaskTitle),
				status:    strings.TrimSpace(event.Message),
			}
		}
	}
	if event.Type == contracts.EventTypeTaskDataUpdated {
		taskID := strings.TrimSpace(event.TaskID)
		if taskID != "" {
			status := normalizeTriageStatus(event.Metadata["triage_status"])
			reason := strings.TrimSpace(event.Metadata["triage_reason"])
			if status != "" || reason != "" {
				m.triage[taskID] = triageState{
					taskID:    taskID,
					taskTitle: strings.TrimSpace(event.TaskTitle),
					status:    status,
					reason:    reason,
				}
			}
		}
	}
	if event.Type == contracts.EventTypeRunStarted {
		m.root.RunID = strings.TrimSpace(event.Metadata["root_id"])
		if !event.Timestamp.IsZero() {
			m.runStartedAt = event.Timestamp
		} else {
			m.runStartedAt = m.now()
		}
		m.runParams = map[string]string{}
		for _, key := range []string{"root_id", "concurrency", "model", "runner_timeout", "stream", "verbose_stream", "stream_output_interval", "stream_output_buffer"} {
			value := strings.TrimSpace(event.Metadata[key])
			if value != "" {
				m.runParams[key] = value
			}
		}
	}
	line := renderHistoryLine(event)
	if line != "" {
		m.history = append(m.history, line)
	}
}

func applyDerivedTaskEvent(task *TaskState, event contracts.Event) {
	if task == nil {
		return
	}
	switch event.Type {
	case contracts.EventTypeRunnerCommandStarted:
		task.CommandStartedCount++
		task.LastCommandStarted = strings.TrimSpace(event.Message)
	case contracts.EventTypeRunnerCommandFinished:
		task.CommandFinishedCount++
		started := strings.TrimSpace(task.LastCommandStarted)
		finished := strings.TrimSpace(event.Message)
		switch {
		case started != "" && finished != "":
			task.LastCommandSummary = started + " -> " + finished
		case finished != "":
			task.LastCommandSummary = finished
		case started != "":
			task.LastCommandSummary = started
		}
	case contracts.EventTypeRunnerOutput:
		task.OutputCount++
	case contracts.EventTypeRunnerWarning:
		task.WarningCount++
		task.WarningActive = true
		task.LastSeverity = "warning"
	case contracts.EventTypeRunnerFinished:
		task.WarningActive = false
		task.TerminalStatus = strings.TrimSpace(event.Message)
		task.LastSeverity = severityFromTerminalStatus(task.TerminalStatus)
	case contracts.EventTypeTaskFinished:
		task.TerminalStatus = strings.TrimSpace(event.Message)
		task.LastSeverity = severityFromTerminalStatus(task.TerminalStatus)
	}
}

func (m *Model) Snapshot() Snapshot {
	workers := map[string]WorkerState{}
	for id, worker := range m.root.Workers {
		workers[id] = worker
	}
	tasks := map[string]TaskState{}
	for id, task := range m.root.Tasks {
		tasks[id] = task
	}
	return Snapshot{Root: RunState{RunID: m.root.RunID, Workers: workers, Tasks: tasks}}
}

func (m *Model) View() string {
	age := "n/a"
	if !m.lastOutputAt.IsZero() {
		seconds := int(m.now().Sub(m.lastOutputAt).Round(time.Second).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		age = fmt.Sprintf("%ds", seconds)
	}
	lines := []string{
		"Run Parameters:",
		"Status Bar:",
		"Current Task: " + renderCurrentTask(m.currentTask, m.currentTitle),
		"Phase: " + emptyAsNA(m.phase),
		"Last Output Age: " + age,
		"Workers:",
	}
	lines = append(lines, renderStatusBar(m.deriveStatusMetrics())...)
	lines = append(lines, renderRunParameters(m.runParams)...)
	lines = append(lines, renderWorkers(m.workers)...)
	lines = append(lines, "Landing Queue:")
	lines = append(lines, renderLandingQueue(m.landing)...)
	lines = append(lines, "Triage:")
	lines = append(lines, renderTriage(m.triage)...)
	lines = append(lines, "History:")
	lines = append(lines, m.history...)
	return strings.Join(lines, "\n") + "\n"
}

type statusMetrics struct {
	runtime           string
	activity          string
	completed         int
	inProgress        int
	blocked           int
	failed            int
	total             int
	queueDepth        int
	workerUtilization int
	throughput        string
	runSeverity       string
	workerSeverity    string
	taskSeverity      string
}

func (m *Model) deriveStatusMetrics() statusMetrics {
	runtimeSeconds := int(m.now().Sub(m.runStartedAt).Round(time.Second).Seconds())
	runtime := "n/a"
	if !m.runStartedAt.IsZero() {
		if runtimeSeconds < 0 {
			runtimeSeconds = 0
		}
		runtime = fmt.Sprintf("%ds", runtimeSeconds)
	}

	metrics := statusMetrics{runtime: runtime, runSeverity: "none", workerSeverity: "none", taskSeverity: "none"}
	for _, task := range m.root.Tasks {
		metrics.total++
		terminal := normalizeTerminalStatus(task.TerminalStatus)
		if isCompletedTerminalStatus(terminal) {
			metrics.completed++
		} else if terminal == "blocked" {
			metrics.blocked++
		} else if terminal == "failed" {
			metrics.failed++
		} else {
			metrics.inProgress++
			if task.QueuePos > 0 {
				metrics.queueDepth++
			}
		}
		metrics.taskSeverity = maxSeverity(metrics.taskSeverity, deriveTaskSeverity(task))
	}

	activeWorkers := 0
	for _, worker := range m.root.Workers {
		severity := "none"
		if task, ok := m.root.Tasks[worker.CurrentTaskID]; ok {
			severity = deriveTaskSeverity(task)
			if !isTerminalStatus(normalizeTerminalStatus(task.TerminalStatus)) {
				activeWorkers++
			}
		}
		metrics.workerSeverity = maxSeverity(metrics.workerSeverity, severity)
	}
	if totalWorkers := len(m.root.Workers); totalWorkers > 0 {
		metrics.workerUtilization = int(math.Round(float64(activeWorkers*100) / float64(totalWorkers)))
	}

	activityState := "idle"
	if metrics.inProgress > 0 {
		activityState = "active"
	}
	spinner := []string{"|", "/", "-", "\\"}
	metrics.activity = fmt.Sprintf("%s(%s)", activityState, spinner[m.eventCount%len(spinner)])

	if runtimeSeconds <= 0 {
		metrics.throughput = fmt.Sprintf("%.2f/s", float64(m.eventCount))
	} else {
		metrics.throughput = fmt.Sprintf("%.2f/s", float64(m.eventCount)/float64(runtimeSeconds))
	}

	metrics.runSeverity = maxSeverity(metrics.taskSeverity, metrics.workerSeverity)
	if m.phase == string(contracts.EventTypeRunnerWarning) {
		metrics.runSeverity = maxSeverity(metrics.runSeverity, "warning")
	}
	return metrics
}

func renderStatusBar(metrics statusMetrics) []string {
	line := fmt.Sprintf("- runtime=%s activity=%s completed=%d in_progress=%d blocked=%d failed=%d total=%d queue_depth=%d worker_utilization=%d%% throughput=%s",
		metrics.runtime,
		metrics.activity,
		metrics.completed,
		metrics.inProgress,
		metrics.blocked,
		metrics.failed,
		metrics.total,
		metrics.queueDepth,
		metrics.workerUtilization,
		metrics.throughput,
	)
	errors := fmt.Sprintf("- errors=run:%s workers:%s tasks:%s", metrics.runSeverity, metrics.workerSeverity, metrics.taskSeverity)
	return []string{line, errors}
}

func normalizeTerminalStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func isCompletedTerminalStatus(status string) bool {
	return status == "completed" || status == "closed"
}

func isTerminalStatus(status string) bool {
	return isCompletedTerminalStatus(status) || status == "blocked" || status == "failed"
}

func severityFromTerminalStatus(status string) string {
	switch normalizeTerminalStatus(status) {
	case "failed":
		return "error"
	case "blocked":
		return "warning"
	case "completed", "closed":
		return "info"
	default:
		return "none"
	}
}

func deriveTaskSeverity(task TaskState) string {
	severity := strings.TrimSpace(task.LastSeverity)
	if severity == "" || severity == "none" {
		severity = severityFromTerminalStatus(task.TerminalStatus)
	}
	if task.WarningActive {
		severity = maxSeverity(severity, "warning")
	}
	if severity == "" {
		return "none"
	}
	return severity
}

func maxSeverity(a string, b string) string {
	rank := map[string]int{"none": 0, "info": 1, "warning": 2, "error": 3}
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if _, ok := rank[a]; !ok {
		a = "none"
	}
	if _, ok := rank[b]; !ok {
		b = "none"
	}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

func renderRunParameters(params map[string]string) []string {
	if len(params) == 0 {
		return []string{"- n/a"}
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, "- "+key+"="+params[key])
	}
	return lines
}

func normalizeTriageStatus(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func renderWorkers(workers map[string]workerLane) []string {
	if len(workers) == 0 {
		return []string{"- n/a"}
	}
	ids := make([]string, 0, len(workers))
	for id := range workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		lane := workers[id]
		current := renderCurrentTask(lane.taskID, lane.taskTitle)
		phase := emptyAsNA(strings.TrimSpace(lane.phase))
		line := fmt.Sprintf("- %s => %s [%s]", id, current, phase)
		if lane.queuePos > 0 {
			line += fmt.Sprintf(" (queue=%d)", lane.queuePos)
		}
		lines = append(lines, line)
	}
	return lines
}

func renderLandingQueue(landing map[string]landingState) []string {
	if len(landing) == 0 {
		return []string{"- n/a"}
	}
	ids := make([]string, 0, len(landing))
	for id := range landing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		entry := landing[id]
		status := emptyAsNA(entry.status)
		lines = append(lines, "- "+renderCurrentTask(entry.taskID, entry.taskTitle)+" => "+status)
	}
	return lines
}

func renderTriage(triage map[string]triageState) []string {
	if len(triage) == 0 {
		return []string{"- n/a"}
	}
	ids := make([]string, 0, len(triage))
	for id := range triage {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		entry := triage[id]
		status := emptyAsNA(entry.status)
		reason := strings.TrimSpace(entry.reason)
		line := "- " + renderCurrentTask(entry.taskID, entry.taskTitle) + " => " + status
		if reason != "" {
			line += " | " + reason
		}
		lines = append(lines, line)
	}
	return lines
}

func emptyAsNA(value string) string {
	if strings.TrimSpace(value) == "" {
		return "n/a"
	}
	return value
}

func renderCurrentTask(id string, title string) string {
	id = strings.TrimSpace(id)
	title = strings.TrimSpace(title)
	if id == "" {
		return "n/a"
	}
	if title == "" {
		return id
	}
	return id + " - " + title
}

func renderHistoryLine(event contracts.Event) string {
	parts := []string{}
	if !event.Timestamp.IsZero() {
		parts = append(parts, event.Timestamp.UTC().Format(time.RFC3339))
	}
	if event.Type != "" {
		parts = append(parts, string(event.Type))
	}
	if event.TaskID != "" {
		parts = append(parts, renderCurrentTask(event.TaskID, event.TaskTitle))
	}
	if event.Message != "" {
		parts = append(parts, event.Message)
	}
	if len(event.Metadata) > 0 {
		keys := make([]string, 0, len(event.Metadata))
		for key := range event.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+"="+event.Metadata[key])
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "- " + strings.Join(parts, " | ")
}
