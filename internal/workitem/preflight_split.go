package workitem

import (
	"unicode"

	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// TaskPayload is the JSON-safe subset of contracts.Task used by model work items.
type TaskPayload struct {
	ID          string               `json:"id"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Status      contracts.TaskStatus `json:"status"`
	ParentID    string               `json:"parent_id"`
	Metadata    map[string]string    `json:"metadata"`
}

type PreflightPayload struct {
	Task      TaskPayload        `json:"task"`
	Comments  []PreflightComment `json:"comments"`
	QueueRoot TaskPayload        `json:"queue_root"`
}

type PreflightComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

type PreflightVerdict string

const (
	PreflightVerdictReady     PreflightVerdict = "ready"
	PreflightVerdictNeedsInfo PreflightVerdict = "needs_info"
	PreflightVerdictReply     PreflightVerdict = "reply"
)

type PreflightResult struct {
	Verdict    PreflightVerdict `json:"verdict"`
	Confidence float64          `json:"confidence"`
	Summary    string           `json:"summary"`
	Questions  []string         `json:"questions"`
	ReplyText  string           `json:"reply_text"`
}

type SplitPayload struct {
	Task         TaskPayload `json:"task"`
	QueueRoot    TaskPayload `json:"queue_root"`
	LanguageHint string      `json:"language_hint"`
}

type SplitResult struct {
	Epics     []splitter.Epic       `json:"epics"`
	Tasks     []splitter.Task       `json:"tasks"`
	Order     []splitter.Dependency `json:"order"`
	RiskNotes []string              `json:"risk_notes"`
}

func TaskPayloadFromTask(task contracts.Task) TaskPayload {
	return TaskPayload{
		ID:          task.ID,
		Title:       task.Title,
		Description: task.Description,
		Status:      task.Status,
		ParentID:    task.ParentID,
		Metadata:    cloneStringMap(task.Metadata),
	}
}

func (p TaskPayload) ToTask() contracts.Task {
	return contracts.Task{
		ID:          p.ID,
		Title:       p.Title,
		Description: p.Description,
		Status:      p.Status,
		ParentID:    p.ParentID,
		Metadata:    cloneStringMap(p.Metadata),
	}
}

func PreflightPayloadFromRunInput(input preflight.RunInput) PreflightPayload {
	return PreflightPayload{
		Task:      TaskPayloadFromTask(input.Task),
		Comments:  preflightCommentsFromRunInput(input.Comments),
		QueueRoot: TaskPayloadFromTask(input.QueueRoot),
	}
}

func (p PreflightPayload) ToRunInput() preflight.RunInput {
	return preflight.RunInput{
		Task:      p.Task.ToTask(),
		Comments:  p.toPreflightComments(),
		QueueRoot: p.QueueRoot.ToTask(),
	}
}

func PreflightResultFromResult(result preflight.Result) PreflightResult {
	return PreflightResult{
		Verdict:    PreflightVerdict(result.Decision),
		Confidence: result.Confidence,
		Summary:    result.Summary,
		Questions:  cloneStringSlice(result.Questions),
		ReplyText:  result.ReplyText,
	}
}

func (r PreflightResult) ToResult() preflight.Result {
	return preflight.Result{
		Decision:   preflight.Decision(r.Verdict),
		Confidence: r.Confidence,
		Summary:    r.Summary,
		Questions:  cloneStringSlice(r.Questions),
		ReplyText:  r.ReplyText,
	}
}

func SplitPayloadFromRunInput(input splitter.RunInput) SplitPayload {
	return SplitPayload{
		Task:         TaskPayloadFromTask(input.Task),
		QueueRoot:    TaskPayloadFromTask(input.QueueRoot),
		LanguageHint: detectedTaskLanguage(input.Task),
	}
}

func (p SplitPayload) ToRunInput() splitter.RunInput {
	return splitter.RunInput{
		Task:      p.Task.ToTask(),
		QueueRoot: p.QueueRoot.ToTask(),
	}
}

func SplitResultFromStrictOutput(output splitter.StrictOutput) SplitResult {
	return SplitResult{
		Epics:     cloneSplitEpics(output.Epics),
		Tasks:     cloneSplitTasks(output.Tasks),
		Order:     cloneSplitOrder(output.Order),
		RiskNotes: cloneStringSlice(output.RiskNotes),
	}
}

func (r SplitResult) ToStrictOutput() splitter.StrictOutput {
	return splitter.StrictOutput{
		Epics:     cloneSplitEpics(r.Epics),
		Tasks:     cloneSplitTasks(r.Tasks),
		Order:     cloneSplitOrder(r.Order),
		RiskNotes: cloneStringSlice(r.RiskNotes),
	}
}

func preflightCommentsFromRunInput(comments []preflight.Comment) []PreflightComment {
	if comments == nil {
		return nil
	}
	out := make([]PreflightComment, len(comments))
	for i, comment := range comments {
		out[i] = PreflightComment{Author: comment.Author, Body: comment.Body}
	}
	return out
}

func (p PreflightPayload) toPreflightComments() []preflight.Comment {
	if p.Comments == nil {
		return nil
	}
	out := make([]preflight.Comment, len(p.Comments))
	for i, comment := range p.Comments {
		out[i] = preflight.Comment{Author: comment.Author, Body: comment.Body}
	}
	return out
}

func cloneSplitEpics(epics []splitter.Epic) []splitter.Epic {
	if epics == nil {
		return nil
	}
	out := make([]splitter.Epic, len(epics))
	copy(out, epics)
	return out
}

func cloneSplitTasks(tasks []splitter.Task) []splitter.Task {
	if tasks == nil {
		return nil
	}
	out := make([]splitter.Task, len(tasks))
	for i, task := range tasks {
		out[i] = splitter.Task{
			ID:            task.ID,
			Title:         task.Title,
			Why:           cloneStringSlice(task.Why),
			InScope:       cloneStringSlice(task.InScope),
			OutOfScope:    cloneStringSlice(task.OutOfScope),
			StrictTDD:     cloneStringSlice(task.StrictTDD),
			DoneWhen:      cloneStringSlice(task.DoneWhen),
			ExpectedFiles: cloneStringSlice(task.ExpectedFiles),
			DependsOn:     cloneStringSlice(task.DependsOn),
			Unlocks:       cloneStringSlice(task.Unlocks),
		}
	}
	return out
}

func cloneSplitOrder(order []splitter.Dependency) []splitter.Dependency {
	if order == nil {
		return nil
	}
	out := make([]splitter.Dependency, len(order))
	copy(out, order)
	return out
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func detectedTaskLanguage(task contracts.Task) string {
	title := task.Title
	description := task.Description
	if containsCyrillic(title) || (title == "" && containsCyrillic(description)) {
		return "Russian"
	}
	if containsLatin(title) || (title == "" && containsLatin(description)) {
		return "English"
	}
	return "the same human language as the parent task"
}

func containsCyrillic(value string) bool {
	for _, r := range value {
		if unicode.In(r, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

func containsLatin(value string) bool {
	for _, r := range value {
		if unicode.In(r, unicode.Latin) {
			return true
		}
	}
	return false
}
