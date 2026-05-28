package startrek

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
)

const startrekDependencyLabelPrefix = "depends-on:"

type IssueCreateOptions struct {
	QueueKey    string
	ParentID    string
	Title       string
	Description string
	Labels      []string
}

type SplitSubtaskCreationTracker interface {
	CreateIssue(ctx context.Context, opts IssueCreateOptions) (Issue, error)
}

type SplitSubtaskCreationService struct {
	Tracker      SplitSubtaskCreationTracker
	ReadyLabel   string
	SubtaskLabel string
}

type SplitSubtasksInput struct {
	QueueKey string
	ParentID string
	Output   splitter.StrictOutput
}

type SplitSubtasksResult struct {
	Issues                []Issue
	IssueIDsBySplitTaskID map[string]string
}

func (s SplitSubtaskCreationService) Create(ctx context.Context, input SplitSubtasksInput) (SplitSubtasksResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Tracker == nil {
		return SplitSubtasksResult{}, errors.New("startrek split subtask tracker is required")
	}

	parentID := strings.TrimSpace(input.ParentID)
	if parentID == "" {
		return SplitSubtasksResult{}, errors.New("startrek parent issue id is required")
	}

	queueKey := fallbackText(input.QueueKey, deriveQueueKey(parentID))
	if queueKey == "" {
		return SplitSubtasksResult{}, errors.New("startrek queue key is required")
	}

	tasks, err := orderedSplitSubtasks(splitSubtasksWithOrderDependencies(input.Output.Tasks, input.Output.Order))
	if err != nil {
		return SplitSubtasksResult{}, err
	}
	if len(tasks) == 0 {
		return SplitSubtasksResult{}, errors.New("split output contains no tasks")
	}

	result := SplitSubtasksResult{
		Issues:                make([]Issue, 0, len(tasks)),
		IssueIDsBySplitTaskID: make(map[string]string, len(tasks)),
	}
	for _, task := range tasks {
		taskID := trimSplitRef(task.ID)
		issue, err := s.Tracker.CreateIssue(ctx, IssueCreateOptions{
			QueueKey:    queueKey,
			ParentID:    parentID,
			Title:       splitSubtaskTitle(task),
			Description: buildSplitSubtaskBody(task),
			Labels:      splitSubtaskLabels(s.effectiveReadyLabel(), s.effectiveSubtaskLabel(), task.DependsOn, result.IssueIDsBySplitTaskID),
		})
		if err != nil {
			return SplitSubtasksResult{}, fmt.Errorf("create startrek subtask for split task %q: %w", taskID, err)
		}

		issueID := strings.TrimSpace(issue.ID)
		if issueID == "" {
			return SplitSubtasksResult{}, fmt.Errorf("create startrek subtask for split task %q returned empty issue id", taskID)
		}
		result.IssueIDsBySplitTaskID[taskID] = issueID
		result.Issues = append(result.Issues, issue)
	}

	return result, nil
}

func (s SplitSubtaskCreationService) effectiveReadyLabel() string {
	return fallbackText(s.ReadyLabel, defaultStorageReadyLabel)
}

func (s SplitSubtaskCreationService) effectiveSubtaskLabel() string {
	return fallbackText(s.SubtaskLabel, startrekSubtaskLabel)
}

func (c *Client) CreateIssue(ctx context.Context, opts IssueCreateOptions) (Issue, error) {
	queueKey := strings.TrimSpace(opts.QueueKey)
	if queueKey == "" {
		return Issue{}, errors.New("startrek queue key is required")
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return Issue{}, errors.New("startrek issue title is required")
	}

	requestBody := startrekIssueCreateRequest{
		Queue:       queueKey,
		Summary:     title,
		Description: strings.TrimSpace(opts.Description),
		Parent:      strings.TrimSpace(opts.ParentID),
		Tags:        normalizedLabels(opts.Labels),
	}
	if requestBody.Description != "" {
		requestBody.MarkupType = "md"
	}

	var rawIssue startrekIssueSearchItem
	if err := c.DoJSON(ctx, http.MethodPost, "issues/", requestBody, &rawIssue); err != nil {
		return Issue{}, fmt.Errorf("create startrek issue %q in queue %q: %w", title, queueKey, err)
	}
	return mapIssue(rawIssue)
}

func (b *StorageBackend) CreateIssue(ctx context.Context, opts IssueCreateOptions) (Issue, error) {
	if b == nil || b.client == nil {
		return Issue{}, errors.New("startrek storage backend is not initialized")
	}
	return b.client.CreateIssue(ctx, opts)
}

type startrekIssueCreateRequest struct {
	Queue       string   `json:"queue"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	MarkupType  string   `json:"markupType,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func orderedSplitSubtasks(tasks []splitter.Task) ([]splitter.Task, error) {
	byID := make(map[string]splitter.Task, len(tasks))
	inputOrder := make([]string, 0, len(tasks))
	for _, task := range tasks {
		id := trimSplitRef(task.ID)
		if id == "" {
			return nil, errors.New("split task id is required")
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf("duplicate split task id %q", id)
		}
		task.ID = id
		byID[id] = task
		inputOrder = append(inputOrder, id)
	}

	visited := make(map[string]bool, len(tasks))
	visiting := make(map[string]bool, len(tasks))
	ordered := make([]splitter.Task, 0, len(tasks))

	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("cyclic split subtask dependency involving %q", id)
		}

		task := byID[id]
		visiting[id] = true
		for _, dependency := range task.DependsOn {
			dependencyID := trimSplitRef(dependency)
			if isSplitNone(dependencyID) {
				continue
			}
			if _, ok := byID[dependencyID]; ok {
				if err := visit(dependencyID); err != nil {
					return err
				}
			}
		}
		visiting[id] = false
		visited[id] = true
		ordered = append(ordered, task)
		return nil
	}

	for _, id := range inputOrder {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func splitSubtasksWithOrderDependencies(tasks []splitter.Task, order []splitter.Dependency) []splitter.Task {
	if len(tasks) == 0 || len(order) == 0 {
		return tasks
	}

	withDependencies := append([]splitter.Task(nil), tasks...)
	taskIndexByID := make(map[string]int, len(withDependencies))
	for i, task := range withDependencies {
		withDependencies[i].DependsOn = append([]string(nil), task.DependsOn...)
		if id := trimSplitRef(task.ID); id != "" {
			taskIndexByID[id] = i
		}
	}

	for _, dependency := range order {
		fromID := trimSplitRef(dependency.From)
		toID := trimSplitRef(dependency.To)
		if fromID == "" || toID == "" || isSplitNone(fromID) || isSplitNone(toID) {
			continue
		}
		toIndex, ok := taskIndexByID[toID]
		if !ok {
			continue
		}
		if _, ok := taskIndexByID[fromID]; !ok {
			continue
		}
		withDependencies[toIndex].DependsOn = appendSplitDependency(withDependencies[toIndex].DependsOn, fromID)
	}
	return withDependencies
}

func appendSplitDependency(dependencies []string, dependencyID string) []string {
	dependencyID = trimSplitRef(dependencyID)
	if dependencyID == "" || isSplitNone(dependencyID) {
		return dependencies
	}
	for _, existing := range dependencies {
		if trimSplitRef(existing) == dependencyID {
			return dependencies
		}
	}
	return append(dependencies, dependencyID)
}

func splitSubtaskTitle(task splitter.Task) string {
	id := trimSplitRef(task.ID)
	title := strings.TrimSpace(task.Title)
	if title == "" {
		return id
	}
	if id == "" || strings.HasPrefix(title, id+" ") {
		return title
	}
	return id + " " + title
}

func buildSplitSubtaskBody(task splitter.Task) string {
	var b strings.Builder
	b.WriteString("### Task: ")
	b.WriteString(splitSubtaskTitle(task))
	b.WriteString("\n\n")

	writeSplitBulletSection(&b, "Why", task.Why)
	writeSplitBulletSection(&b, "In scope", task.InScope)
	writeSplitBulletSection(&b, "Out of scope", task.OutOfScope)
	writeSplitNumberedSection(&b, "Strict TDD", task.StrictTDD)
	writeSplitBulletSection(&b, "Done when", task.DoneWhen)
	writeSplitBulletSection(&b, "Expected files", task.ExpectedFiles)
	writeSplitBulletSection(&b, "Depends on", task.DependsOn)
	writeSplitBulletSection(&b, "Unlocks", task.Unlocks)

	return strings.TrimSpace(b.String())
}

func writeSplitBulletSection(b *strings.Builder, label string, items []string) {
	b.WriteString(label)
	b.WriteString(":\n")
	normalized := normalizedSplitItems(items)
	if len(normalized) == 0 {
		b.WriteString("- none\n\n")
		return
	}
	for _, item := range normalized {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

func writeSplitNumberedSection(b *strings.Builder, label string, items []string) {
	b.WriteString(label)
	b.WriteString(":\n")
	normalized := normalizedSplitItems(items)
	if len(normalized) == 0 {
		b.WriteString("1. none\n\n")
		return
	}
	for i, item := range normalized {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, item))
	}
	b.WriteByte('\n')
}

func normalizedSplitItems(items []string) []string {
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			normalized = append(normalized, item)
		}
	}
	return normalized
}

func splitSubtaskLabels(readyLabel string, subtaskLabel string, dependsOn []string, createdIssueIDs map[string]string) []string {
	labels := make([]string, 0, 2+len(dependsOn))
	seen := map[string]struct{}{}
	appendLabel := func(label string) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		if _, ok := seen[label]; ok {
			return
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}

	appendLabel(readyLabel)
	appendLabel(subtaskLabel)

	for _, dependency := range dependsOn {
		dependencyID := splitDependencyIssueID(dependency, createdIssueIDs)
		if dependencyID == "" {
			continue
		}
		appendLabel(startrekDependencyLabelPrefix + dependencyID)
	}
	return labels
}

func splitDependencyIssueID(dependency string, createdIssueIDs map[string]string) string {
	dependency = trimSplitRef(dependency)
	if dependency == "" || isSplitNone(dependency) {
		return ""
	}
	if issueID := strings.TrimSpace(createdIssueIDs[dependency]); issueID != "" {
		return issueID
	}
	if match := startrekIssueKeyPattern.FindString(dependency); match != "" {
		return match
	}
	return ""
}

func trimSplitRef(value string) string {
	return strings.Trim(strings.TrimSpace(value), "`")
}

func isSplitNone(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "n/a", "na":
		return true
	default:
		return false
	}
}
