package monitor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type Model struct {
	now          func() time.Time
	currentTask  string
	currentTitle string
	phase        string
	lastOutputAt time.Time
	history      []string
	workers      map[string]workerLane
	landing      map[string]landingState
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

func NewModel(now func() time.Time) *Model {
	if now == nil {
		now = time.Now
	}
	return &Model{
		now:     now,
		history: []string{},
		workers: map[string]workerLane{},
		landing: map[string]landingState{},
	}
}

func (m *Model) Apply(event contracts.Event) {
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
		m.workers[workerID] = workerLane{
			taskID:    event.TaskID,
			taskTitle: event.TaskTitle,
			phase:     string(event.Type),
			queuePos:  event.QueuePos,
		}
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
	line := renderHistoryLine(event)
	if line != "" {
		m.history = append(m.history, line)
	}
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
		"Current Task: " + renderCurrentTask(m.currentTask, m.currentTitle),
		"Phase: " + emptyAsNA(m.phase),
		"Last Output Age: " + age,
		"Workers:",
	}
	lines = append(lines, renderWorkers(m.workers)...)
	lines = append(lines, "Landing Queue:")
	lines = append(lines, renderLandingQueue(m.landing)...)
	lines = append(lines, "History:")
	lines = append(lines, m.history...)
	return strings.Join(lines, "\n") + "\n"
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
