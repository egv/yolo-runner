package monitor

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type Model struct {
	now                func() time.Time
	root               RunState
	runStartedAt       time.Time
	eventCount         int
	panelCursor        int
	panelExpand        map[string]bool
	historyLimit       int
	panelRowLimit      int
	viewportHeight     int
	panelRowsCache     []panelRow
	panelRowsDirty     bool
	panelRowsTruncated bool
	runParams          map[string]string
	currentTask        string
	currentTitle       string
	phase              string
	lastOutputAt       time.Time
	history            []string
	workers            map[string]workerLane
	landing            map[string]landingState
	triage             map[string]triageState
	queueFilter        string
}

type Snapshot struct {
	Root RunState
}

type PerformanceSnapshot struct {
	HistorySize        int
	TotalPanelRows     int
	VisiblePanelRows   int
	PanelRowsTruncated bool
}

type UIState struct {
	CurrentTask       string
	Phase             string
	LastOutputAge     string
	StatusSummary     string
	StatusMetrics     statusMetrics
	StatusBar         []string
	Performance       []string
	PanelLines        []UIPanelLine
	RunParams         []string
	WorkerSummaries   []UIWorkerSummary
	Queue             []string
	QueueFilter       string
	TaskGraph         []string
	TaskDetails       []string
	ExecutorDashboard []string
	Landing           []string
	Triage            []string
	History           []string
}

type UIPanelLine struct {
	ID        string
	Depth     int
	Label     string
	Completed bool
	Severity  string
	Selected  bool
	Expanded  bool
	Leaf      bool
}

type UIWorkerSummary struct {
	WorkerID         string
	Task             string
	Phase            string
	QueuePos         int
	TaskPriority     int
	LastEvent        string
	LastActivityAge  string
	Severity         string
	RecentTaskEvents []string
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
	Priority             int
	ParentID             string
	Dependencies         []string
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

const (
	queueFilterAll    = "all"
	queueFilterActive = "active"
)

func NewModel(now func() time.Time) *Model {
	if now == nil {
		now = time.Now
	}
	return &Model{
		now:  now,
		root: RunState{Workers: map[string]WorkerState{}, Tasks: map[string]TaskState{}},
		panelExpand: map[string]bool{
			"run":     true,
			"workers": true,
			"tasks":   false,
			"queue":   false,
			"graph":   false,
		},
		historyLimit:   256,
		panelRowLimit:  512,
		viewportHeight: 32,
		panelRowsCache: []panelRow{},
		panelRowsDirty: true,
		runParams:      map[string]string{},
		history:        []string{},
		workers:        map[string]workerLane{},
		landing:        map[string]landingState{},
		triage:         map[string]triageState{},
		queueFilter:    queueFilterAll,
	}
}

func (m *Model) SetPerformanceControls(historyLimit int, panelRowLimit int) {
	if historyLimit <= 0 {
		historyLimit = 1
	}
	if panelRowLimit <= 0 {
		panelRowLimit = 1
	}
	m.historyLimit = historyLimit
	m.panelRowLimit = panelRowLimit
	if len(m.history) > m.historyLimit {
		m.history = append([]string{}, m.history[len(m.history)-m.historyLimit:]...)
	}
	m.panelRowsDirty = true
}

func (m *Model) SetViewportHeight(height int) {
	if height <= 0 {
		height = 1
	}
	m.viewportHeight = height
}

func (m *Model) PerformanceSnapshot() PerformanceSnapshot {
	rows := m.panelRows()
	visible := len(rows)
	if m.viewportHeight > 0 && visible > m.viewportHeight {
		visible = m.viewportHeight
	}
	return PerformanceSnapshot{
		HistorySize:        len(m.history),
		TotalPanelRows:     len(rows),
		VisiblePanelRows:   visible,
		PanelRowsTruncated: m.panelRowsTruncated || len(rows) > visible,
	}
}

func (m *Model) HandleKey(key string) {
	rows := m.panelRows()
	if key == " " {
		key = "space"
	}
	if len(rows) == 0 {
		m.panelCursor = 0
		return
	}
	if m.panelCursor < 0 {
		m.panelCursor = 0
	}
	if m.panelCursor >= len(rows) {
		m.panelCursor = len(rows) - 1
	}
	current := rows[m.panelCursor]
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "down", "j":
		if m.panelCursor < len(rows)-1 {
			m.panelCursor++
		}
	case "up", "k":
		if m.panelCursor > 0 {
			m.panelCursor--
		}
	case "enter", "space":
		if current.hasChildren {
			m.panelExpand[current.id] = !current.expanded
			m.panelRowsDirty = true
		}
	case "right", "l":
		if current.hasChildren {
			m.panelExpand[current.id] = true
			m.panelRowsDirty = true
		}
	case "left", "h":
		if current.hasChildren {
			m.panelExpand[current.id] = false
			m.panelRowsDirty = true
		}
	case "f":
		m.CycleQueueFilter()
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
		if event.Priority != 0 {
			task.Priority = event.Priority
		}
		if metadataParent := normalizeTaskMetadataString(event.Metadata["parent_id"]); metadataParent != "" {
			task.ParentID = metadataParent
		}
		if parsedDependencies := parseTaskDependencies(event.Metadata["dependencies"]); len(parsedDependencies) > 0 {
			task.Dependencies = mergeTaskDependencies(task.Dependencies, parsedDependencies)
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
		for _, key := range []string{"root_id", "concurrency", "model", "runner_timeout", "watchdog_timeout", "watchdog_interval", "stream", "verbose_stream", "stream_output_interval", "stream_output_buffer"} {
			value := strings.TrimSpace(event.Metadata[key])
			if value != "" {
				m.runParams[key] = value
			}
		}
	}
	line := renderHistoryLine(event)
	if line != "" {
		m.history = append(m.history, line)
		if len(m.history) > m.historyLimit {
			m.history = append([]string{}, m.history[len(m.history)-m.historyLimit:]...)
		}
	}
	m.panelRowsDirty = true
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
	case contracts.EventTypeRunnerHeartbeat:
		activeCommand := strings.TrimSpace(task.LastCommandStarted)
		lastOutputAge := strings.TrimSpace(event.Metadata["last_output_age"])
		switch {
		case activeCommand != "" && lastOutputAge != "":
			task.LastMessage = "active: " + activeCommand + " (last output " + lastOutputAge + ")"
		case activeCommand != "":
			task.LastMessage = "active: " + activeCommand
		case lastOutputAge != "":
			task.LastMessage = "heartbeat: last output " + lastOutputAge
		}
	case contracts.EventTypeRunnerWarning:
		task.WarningCount++
		task.WarningActive = true
		task.LastSeverity = "warning"
	case contracts.EventTypeRunnerFinished:
		task.WarningActive = false
		task.TerminalStatus = strings.TrimSpace(event.Message)
		task.LastSeverity = severityFromTerminalStatus(task.TerminalStatus)
	case contracts.EventTypeReviewFinished:
		if reason := strings.TrimSpace(event.Metadata["reason"]); reason != "" {
			task.LastMessage = strings.TrimSpace(event.Message) + " | " + reason
		}
	case contracts.EventTypeTaskFinished:
		task.TerminalStatus = strings.TrimSpace(event.Message)
		task.LastSeverity = severityFromTerminalStatus(task.TerminalStatus)
		if reason := strings.TrimSpace(event.Metadata["triage_reason"]); reason != "" {
			task.LastMessage = task.TerminalStatus + " | " + reason
		}
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

func (m *Model) UIState() UIState {
	metrics := m.deriveStatusMetrics()
	selectedTaskID := m.currentPanelTaskID()
	if selectedTaskID == "" && strings.TrimSpace(m.currentTask) != "" {
		selectedTaskID = m.currentTask
	}
	taskDetails := renderTaskDetails(m.root.Tasks[selectedTaskID])
	return UIState{
		CurrentTask:       renderCurrentTask(m.currentTask, m.currentTitle),
		Phase:             emptyAsNA(m.phase),
		LastOutputAge:     ageSince(m.now(), m.lastOutputAt),
		StatusSummary:     fmt.Sprintf("⏱ %s  %s  ✅%d  🟡%d  ❌%d/%d  👷 %d%%  📦 %d  ⚡ %s", metrics.runtime, metrics.activity, metrics.completed, metrics.blocked, metrics.failed, metrics.total, metrics.workerUtilization, metrics.queueDepth, metrics.throughput),
		StatusMetrics:     metrics,
		StatusBar:         renderStatusBar(metrics),
		Performance:       renderPerformance(m.PerformanceSnapshot()),
		PanelLines:        m.uiPanelLines(),
		RunParams:         renderRunParameters(m.runParams),
		WorkerSummaries:   m.uiWorkerSummaries(),
		Queue:             renderQueueRows(m.root.Tasks, m.queueFilter),
		QueueFilter:       normalizeQueueFilter(m.queueFilter),
		TaskGraph:         renderTaskGraphRows(m.root.Tasks),
		TaskDetails:       taskDetails,
		ExecutorDashboard: renderExecutorDashboard(metrics, m.root.Workers, m.root.Tasks, m.queueFilter),
		Landing:           renderLandingQueue(m.landing),
		Triage:            renderTriage(m.triage),
		History:           append([]string{}, m.history...),
	}
}

func (m *Model) uiPanelLines() []UIPanelLine {
	rows := m.panelRows()
	if len(rows) == 0 {
		return []UIPanelLine{}
	}
	cursor := m.panelCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	start, end := panelWindow(len(rows), cursor, m.viewportHeight)
	lines := make([]UIPanelLine, 0, end-start)
	for i := start; i < end; i++ {
		row := rows[i]
		lines = append(lines, UIPanelLine{
			ID:        row.id,
			Depth:     row.indent,
			Label:     row.label,
			Completed: row.completed,
			Severity:  row.severity,
			Selected:  i == cursor,
			Expanded:  row.expanded,
			Leaf:      !row.hasChildren,
		})
	}
	if hidden := len(rows) - end; hidden > 0 {
		lines = append(lines, UIPanelLine{ID: "more", Depth: 0, Label: fmt.Sprintf("... %d more panel rows", hidden), Severity: "none", Leaf: true})
	}
	return lines
}

func (m *Model) uiWorkerSummaries() []UIWorkerSummary {
	ids := sortedWorkerIDs(m.root.Workers)
	workers := make([]UIWorkerSummary, 0, len(ids))
	for _, workerID := range ids {
		worker := m.root.Workers[workerID]
		task := m.root.Tasks[worker.CurrentTaskID]
		lastEvent := strings.TrimSpace(task.LastMessage)
		if lastEvent == "" {
			lastEvent = emptyAsNA(task.RunnerPhase)
		}
		recent := []string{}
		if summary := strings.TrimSpace(task.LastCommandSummary); summary != "" {
			recent = append(recent, "🛠 "+summary)
		}
		if msg := strings.TrimSpace(task.LastMessage); msg != "" {
			recent = append(recent, "📌 "+msg)
		}
		if status := strings.TrimSpace(task.TerminalStatus); status != "" {
			recent = append(recent, "🏁 "+status)
		}
		if len(recent) == 0 {
			recent = append(recent, "📭 no task events yet")
		}
		workers = append(workers, UIWorkerSummary{
			WorkerID:         workerID,
			Task:             renderCurrentTask(task.TaskID, task.Title),
			Phase:            emptyAsNA(worker.CurrentPhase),
			QueuePos:         worker.CurrentQueuePos,
			TaskPriority:     task.Priority,
			LastEvent:        lastEvent,
			LastActivityAge:  ageSince(m.now(), task.LastUpdateAt),
			Severity:         deriveTaskSeverity(task),
			RecentTaskEvents: recent,
		})
	}
	return workers
}

func ageSince(now time.Time, when time.Time) string {
	if when.IsZero() {
		return "n/a"
	}
	seconds := int(now.Sub(when).Round(time.Second).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%ds", seconds)
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
		"Executor Dashboard:",
		"Performance:",
		"Panels:",
		"Current Task: " + renderCurrentTask(m.currentTask, m.currentTitle),
		"Phase: " + emptyAsNA(m.phase),
		"Last Output Age: " + age,
		"Workers:",
	}
	perf := m.PerformanceSnapshot()
	metrics := m.deriveStatusMetrics()
	lines = append(lines, renderStatusBar(metrics)...)
	lines = append(lines, renderExecutorDashboard(metrics, m.root.Workers, m.root.Tasks, m.queueFilter)...)
	lines = append(lines, renderPerformance(perf)...)
	lines = append(lines, renderPanels(m.panelRows(), m.panelCursor, m.viewportHeight)...)
	lines = append(lines, renderRunParameters(m.runParams)...)
	lines = append(lines, "Queue:")
	lines = append(lines, "- filter="+normalizeQueueFilter(m.queueFilter))
	lines = append(lines, renderQueueRows(m.root.Tasks, m.queueFilter)...)
	lines = append(lines, "Task Graph:")
	lines = append(lines, renderTaskGraphRows(m.root.Tasks)...)
	lines = append(lines, renderWorkers(m.workers)...)
	lines = append(lines, "Landing Queue:")
	lines = append(lines, renderLandingQueue(m.landing)...)
	lines = append(lines, "Triage:")
	lines = append(lines, renderTriage(m.triage)...)
	lines = append(lines, "History:")
	lines = append(lines, m.history...)
	return strings.Join(lines, "\n") + "\n"
}

type panelRow struct {
	id          string
	indent      int
	label       string
	completed   bool
	severity    string
	hasChildren bool
	expanded    bool
}

func (m *Model) panelRows() []panelRow {
	if !m.panelRowsDirty {
		return m.panelRowsCache
	}
	metrics := m.deriveStatusMetrics()
	rows := []panelRow{}
	runRow := panelRow{id: "run", label: "Run", severity: metrics.runSeverity, hasChildren: true, expanded: m.isPanelExpanded("run")}
	rows = append(rows, runRow)
	if !runRow.expanded {
		return rows
	}

	workersRow := panelRow{id: "workers", indent: 1, label: "Workers", severity: metrics.workerSeverity, hasChildren: true, expanded: m.isPanelExpanded("workers")}
	tasksRow := panelRow{id: "tasks", indent: 1, label: "Tasks", severity: metrics.taskSeverity, hasChildren: true, expanded: m.isPanelExpanded("tasks")}
	queueRow := panelRow{id: "queue", indent: 1, label: "Queue", severity: metrics.taskSeverity, hasChildren: true, expanded: m.isPanelExpanded("queue")}
	graphRow := panelRow{id: "graph", indent: 1, label: "Task Graph", severity: metrics.taskSeverity, hasChildren: true, expanded: m.isPanelExpanded("graph")}
	rows = append(rows, workersRow)

	if workersRow.expanded {
		for _, workerID := range sortedWorkerIDs(m.root.Workers) {
			workerSeverity := "none"
			for _, task := range tasksForWorker(m.root.Tasks, workerID) {
				workerSeverity = maxSeverity(workerSeverity, deriveTaskSeverity(task))
			}
			workerIDRow := "worker:" + workerID
			workerRow := panelRow{id: workerIDRow, indent: 2, label: workerID, severity: workerSeverity, hasChildren: true, expanded: m.isPanelExpanded(workerIDRow)}
			rows = append(rows, workerRow)
			if workerRow.expanded {
				for _, task := range tasksForWorker(m.root.Tasks, workerID) {
					rows = append(rows, panelRow{
						id:          "worker:" + workerID + ":task:" + task.TaskID,
						indent:      3,
						label:       renderTaskPanelLabel(task),
						completed:   isTaskCompleted(task),
						severity:    deriveTaskSeverity(task),
						hasChildren: false,
					})
				}
			}
		}
	}

	rows = append(rows, tasksRow)

	if tasksRow.expanded {
		for _, taskID := range sortedTaskIDs(m.root.Tasks) {
			task := m.root.Tasks[taskID]
			rows = append(rows, panelRow{
				id:          "task:" + taskID,
				indent:      2,
				label:       renderTaskPanelLabel(task),
				completed:   isTaskCompleted(task),
				severity:    deriveTaskSeverity(task),
				hasChildren: false,
			})
		}
	}

	rows = append(rows, queueRow)
	if queueRow.expanded {
		for _, taskID := range sortedQueueTaskIDs(m.root.Tasks, m.queueFilter) {
			task := m.root.Tasks[taskID]
			rows = append(rows, panelRow{
				id:          "queue:task:" + task.TaskID,
				indent:      2,
				label:       renderQueueRowLabel(task),
				completed:   isTaskCompleted(task),
				severity:    deriveTaskSeverity(task),
				hasChildren: false,
			})
		}
	}

	rows = append(rows, graphRow)
	if graphRow.expanded {
		for _, row := range renderGraphPanelRows(m.root.Tasks) {
			rows = append(rows, row)
		}
	}

	m.panelRowsTruncated = false
	if len(rows) > m.panelRowLimit {
		rows = append([]panelRow{}, rows[:m.panelRowLimit]...)
		m.panelRowsTruncated = true
	}
	m.panelRowsCache = rows
	m.panelRowsDirty = false
	if len(m.panelRowsCache) == 0 {
		m.panelRowsCache = []panelRow{}
	}
	return m.panelRowsCache
}

func (m *Model) isPanelExpanded(panelID string) bool {
	expanded, ok := m.panelExpand[panelID]
	if ok {
		return expanded
	}
	if strings.HasPrefix(panelID, "worker:") {
		return false
	}
	return false
}

func renderPanels(rows []panelRow, cursor int, viewportHeight int) []string {
	if len(rows) == 0 {
		return []string{"- n/a"}
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(rows) {
		cursor = len(rows) - 1
	}
	start, end := panelWindow(len(rows), cursor, viewportHeight)
	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		row := rows[i]
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		glyph := "[ ]"
		if row.hasChildren {
			if row.expanded {
				glyph = "[-]"
			} else {
				glyph = "[+]"
			}
		}
		indent := strings.Repeat("  ", row.indent)
		if i == cursor {
			indent = ""
		}
		line := marker + indent + glyph + " " + row.label
		if severity := strings.TrimSpace(row.severity); severity != "" && severity != "none" {
			line += " severity=" + severity
		}
		lines = append(lines, line)
	}
	if hidden := len(rows) - end; hidden > 0 {
		lines = append(lines, fmt.Sprintf("- ... %d more panel rows", hidden))
	}
	return lines
}

func panelWindow(total int, cursor int, viewportHeight int) (int, int) {
	if viewportHeight <= 0 || viewportHeight >= total {
		return 0, total
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= total {
		cursor = total - 1
	}
	half := viewportHeight / 2
	start := cursor - half
	if start < 0 {
		start = 0
	}
	end := start + viewportHeight
	if end > total {
		end = total
		start = end - viewportHeight
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func renderPerformance(perf PerformanceSnapshot) []string {
	line := fmt.Sprintf("- history_size=%d panel_rows=%d/%d truncated=%t", perf.HistorySize, perf.VisiblePanelRows, perf.TotalPanelRows, perf.PanelRowsTruncated)
	return []string{line}
}

func sortedWorkerIDs(workers map[string]WorkerState) []string {
	ids := make([]string, 0, len(workers))
	for id := range workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedTaskIDs(tasks map[string]TaskState) []string {
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func tasksForWorker(tasks map[string]TaskState, workerID string) []TaskState {
	workerTasks := make([]TaskState, 0)
	for _, task := range tasks {
		if task.WorkerID == workerID {
			workerTasks = append(workerTasks, task)
		}
	}
	sort.Slice(workerTasks, func(i int, j int) bool {
		return workerTasks[i].TaskID < workerTasks[j].TaskID
	})
	return workerTasks
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
	return status == "completed" || status == "closed" || status == "done"
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
	case "completed", "closed", "done":
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

func renderQueueRows(tasks map[string]TaskState, filter string) []string {
	if len(tasks) == 0 {
		return []string{"- n/a"}
	}
	ids := sortedQueueTaskIDs(tasks, filter)
	if len(ids) == 0 {
		return []string{"- n/a"}
	}
	lines := make([]string, 0, len(ids))
	for _, taskID := range ids {
		lines = append(lines, "- "+renderQueueRowLabel(tasks[taskID]))
	}
	return lines
}

func renderTaskGraphRows(tasks map[string]TaskState) []string {
	lines := renderGraphRows(tasks)
	if len(lines) == 0 {
		return []string{"- n/a"}
	}
	return lines
}

func renderQueueRowLabel(task TaskState) string {
	line := renderCurrentTask(task.TaskID, task.Title)
	parts := []string{}
	if task.QueuePos > 0 {
		parts = append(parts, fmt.Sprintf("q=%d", task.QueuePos))
	}
	if task.Priority != 0 {
		parts = append(parts, fmt.Sprintf("p=%d", task.Priority))
	}
	if len(parts) == 0 {
		return line
	}
	return line + " [" + strings.Join(parts, ", ") + "]"
}

func sortedQueueTaskIDs(tasks map[string]TaskState, filter string) []string {
	filter = normalizeQueueFilter(filter)
	ids := make([]string, 0, len(tasks))
	for id, task := range tasks {
		if !taskMatchesQueueFilter(task, filter) {
			continue
		}
		if task.QueuePos > 0 || task.Priority != 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		for id := range tasks {
			if !taskMatchesQueueFilter(tasks[id], filter) {
				continue
			}
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i int, j int) bool {
		left := tasks[ids[i]]
		right := tasks[ids[j]]
		if left.QueuePos != right.QueuePos {
			return left.QueuePos < right.QueuePos
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		return left.TaskID < right.TaskID
	})
	return ids
}

func normalizeQueueFilter(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", queueFilterAll:
		return queueFilterAll
	case queueFilterActive:
		return queueFilterActive
	default:
		return queueFilterAll
	}
}

func (m *Model) CycleQueueFilter() {
	if m == nil {
		return
	}
	switch normalizeQueueFilter(m.queueFilter) {
	case queueFilterAll:
		m.queueFilter = queueFilterActive
	default:
		m.queueFilter = queueFilterAll
	}
	m.panelRowsDirty = true
}

func taskMatchesQueueFilter(task TaskState, filter string) bool {
	switch normalizeQueueFilter(filter) {
	case queueFilterActive:
		return !isTerminalStatus(normalizeTerminalStatus(task.TerminalStatus))
	default:
		return true
	}
}

func renderExecutorDashboard(metrics statusMetrics, workers map[string]WorkerState, tasks map[string]TaskState, queueFilter string) []string {
	totalWorkers := len(workers)
	activeWorkers := 0
	for _, worker := range workers {
		task, ok := tasks[worker.CurrentTaskID]
		if ok && !isTerminalStatus(normalizeTerminalStatus(task.TerminalStatus)) {
			activeWorkers++
		}
	}
	idleWorkers := totalWorkers - activeWorkers
	if idleWorkers < 0 {
		idleWorkers = 0
	}
	queueTotal := len(sortedQueueTaskIDs(tasks, queueFilterAll))
	queueVisible := len(sortedQueueTaskIDs(tasks, queueFilter))
	return []string{
		fmt.Sprintf("- workers_total=%d workers_active=%d workers_idle=%d", totalWorkers, activeWorkers, idleWorkers),
		fmt.Sprintf("- queue_filter=%s queue_visible=%d queue_total=%d", normalizeQueueFilter(queueFilter), queueVisible, queueTotal),
		fmt.Sprintf("- tasks_total=%d completed=%d in_progress=%d blocked=%d failed=%d", metrics.total, metrics.completed, metrics.inProgress, metrics.blocked, metrics.failed),
		fmt.Sprintf("- worker_utilization=%d%% throughput=%s", metrics.workerUtilization, metrics.throughput),
	}
}

func renderGraphRows(tasks map[string]TaskState) []string {
	children, roots := buildTaskDependencyGraph(tasks)
	out := make([]string, 0, len(tasks))
	visited := map[string]struct{}{}
	for _, taskID := range roots {
		emitGraphLines(tasks, children, taskID, 0, visited, &out)
	}
	for _, taskID := range sortedTaskIDs(tasks) {
		if _, seen := visited[taskID]; seen {
			continue
		}
		emitGraphLines(tasks, children, taskID, 0, visited, &out)
	}
	return out
}

func emitGraphLines(tasks map[string]TaskState, children map[string][]string, taskID string, depth int, visited map[string]struct{}, out *[]string) {
	if _, seen := visited[taskID]; seen {
		return
	}
	task, ok := tasks[taskID]
	if !ok {
		return
	}
	visited[taskID] = struct{}{}
	line := strings.Repeat("  ", depth) + renderCurrentTask(task.TaskID, task.Title)
	*out = append(*out, line)
	for _, childID := range children[taskID] {
		emitGraphLines(tasks, children, childID, depth+1, visited, out)
	}
}

func renderGraphPanelRows(tasks map[string]TaskState) []panelRow {
	out := make([]panelRow, 0, len(tasks))
	children, roots := buildTaskDependencyGraph(tasks)
	visited := map[string]struct{}{}
	for _, rootID := range roots {
		emitGraphPanelRows(tasks, children, rootID, 2, visited, &out)
	}
	for _, taskID := range sortedTaskIDs(tasks) {
		if _, seen := visited[taskID]; seen {
			continue
		}
		emitGraphPanelRows(tasks, children, taskID, 2, visited, &out)
	}
	return out
}

func buildTaskDependencyGraph(tasks map[string]TaskState) (map[string][]string, []string) {
	children := map[string][]string{}
	roots := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskID := strings.TrimSpace(task.TaskID)
		if taskID == "" {
			continue
		}

		parents := taskDependencyParents(task)
		if len(parents) == 0 {
			roots = append(roots, taskID)
			continue
		}

		hasKnownParent := false
		primaryParent := ""
		for _, parentID := range parents {
			parentID = strings.TrimSpace(parentID)
			if parentID == "" {
				continue
			}
			if parentID == taskID {
				continue
			}
			if _, exists := tasks[parentID]; !exists {
				continue
			}
			if primaryParent == "" {
				primaryParent = parentID
			}
			hasKnownParent = true
		}
		if !hasKnownParent {
			roots = append(roots, taskID)
			continue
		}
		children[primaryParent] = append(children[primaryParent], taskID)
	}
	for parentID := range children {
		sort.Strings(children[parentID])
	}
	sort.Strings(roots)
	return children, roots
}

func taskDependencyParents(task TaskState) []string {
	dependencies := dedupeTaskDependencies(task.Dependencies)
	if len(dependencies) > 0 {
		return dependencies
	}
	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" {
		return nil
	}
	return []string{parentID}
}

func emitGraphPanelRows(tasks map[string]TaskState, children map[string][]string, taskID string, depth int, visited map[string]struct{}, out *[]panelRow) {
	if _, seen := visited[taskID]; seen {
		return
	}
	task, ok := tasks[taskID]
	if !ok {
		return
	}
	visited[taskID] = struct{}{}
	*out = append(*out, panelRow{
		id:          "graph:" + taskID,
		indent:      depth,
		label:       renderCurrentTask(task.TaskID, task.Title),
		completed:   isTaskCompleted(task),
		severity:    deriveTaskSeverity(task),
		hasChildren: len(children[taskID]) > 0,
	})
	for _, childID := range children[taskID] {
		emitGraphPanelRows(tasks, children, childID, depth+1, visited, out)
	}
}

func renderTaskDetails(task TaskState) []string {
	if task.TaskID == "" {
		return []string{"- no task selected"}
	}
	lines := []string{
		"- task=" + renderCurrentTask(task.TaskID, task.Title),
	}
	if workerID := strings.TrimSpace(task.WorkerID); workerID != "" {
		lines = append(lines, "  worker="+workerID)
	}
	lines = append(lines, fmt.Sprintf("  queue_pos=%d priority=%d", task.QueuePos, task.Priority))
	if task.ParentID != "" {
		lines = append(lines, "  parent="+task.ParentID)
	}
	if len(task.Dependencies) > 0 {
		lines = append(lines, "  dependencies="+strings.Join(task.Dependencies, ", "))
	} else {
		lines = append(lines, "  dependencies=n/a")
	}
	if task.CommandStartedCount > 0 {
		lines = append(lines, fmt.Sprintf("  cmd_started=%d cmd_finished=%d outputs=%d warnings=%d", task.CommandStartedCount, task.CommandFinishedCount, task.OutputCount, task.WarningCount))
	}
	if task.LastCommandSummary != "" {
		lines = append(lines, "  last_cmd="+task.LastCommandSummary)
	}
	if task.LastMessage != "" {
		lines = append(lines, "  last_message="+task.LastMessage)
	}
	if task.TerminalStatus != "" {
		lines = append(lines, "  terminal="+task.TerminalStatus)
	}
	return lines
}

func (m *Model) currentPanelTaskID() string {
	if m == nil {
		return ""
	}
	if len(m.panelRows()) == 0 {
		return ""
	}
	cursor := m.panelCursor
	if cursor < 0 || cursor >= len(m.panelRows()) {
		return ""
	}
	selectedID := m.panelRows()[cursor].id
	return panelTaskIDFromRow(selectedID)
}

func panelTaskIDFromRow(rowID string) string {
	if strings.HasPrefix(rowID, "task:") {
		return strings.TrimPrefix(rowID, "task:")
	}
	if strings.HasPrefix(rowID, "worker:") && strings.Contains(rowID, ":task:") {
		return strings.TrimPrefix(rowID[strings.Index(rowID, ":task:"):], ":task:")
	}
	if strings.HasPrefix(rowID, "queue:task:") {
		return strings.TrimPrefix(rowID, "queue:task:")
	}
	if strings.HasPrefix(rowID, "graph:") {
		return strings.TrimPrefix(rowID, "graph:")
	}
	return ""
}

func normalizeTaskMetadataString(raw string) string {
	return strings.TrimSpace(raw)
}

func parseTaskDependencies(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		dep := strings.TrimSpace(part)
		if dep == "" {
			continue
		}
		out = append(out, dep)
	}
	return dedupeTaskDependencies(out)
}

func mergeTaskDependencies(existing []string, incoming []string) []string {
	all := append(append(make([]string, 0, len(existing)+len(incoming)), existing...), incoming...)
	return dedupeTaskDependencies(all)
}

func dedupeTaskDependencies(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, dep := range raw {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if _, exists := seen[dep]; exists {
			continue
		}
		seen[dep] = struct{}{}
		out = append(out, dep)
	}
	return out
}

func dedupeSortedDependencies(left []string, right []string) []string {
	merged := make([]string, 0, len(left)+len(right))
	seen := map[string]struct{}{}
	for _, dep := range append(left, right...) {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if _, ok := seen[dep]; ok {
			continue
		}
		seen[dep] = struct{}{}
		merged = append(merged, dep)
	}
	sort.Strings(merged)
	return merged
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

func renderTaskPanelLabel(task TaskState) string {
	label := renderCurrentTask(task.TaskID, task.Title)
	if isTaskCompleted(task) {
		return "✅ " + label
	}
	return label
}

func isTaskCompleted(task TaskState) bool {
	return isCompletedTerminalStatus(normalizeTerminalStatus(task.TerminalStatus))
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
