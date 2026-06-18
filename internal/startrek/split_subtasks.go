package startrek

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
)

const startrekDependsOnRelationship = "depends_on"

var (
	markdownDocLinkPattern = regexp.MustCompile(`\[[^\]\n]+\]\((https?://[^)\s]+)\)`)
	bareDocLinkPattern     = regexp.MustCompile(`https?://[^\s<>()\]]+`)
)

type IssueCreateOptions struct {
	QueueKey    string
	ParentID    string
	Title       string
	Description string
	Labels      []string
}

type IssueLinkCreateOptions struct {
	IssueID        string
	Relationship   string
	RelatedIssueID string
}

type SplitSubtaskCreationTracker interface {
	CreateIssue(ctx context.Context, opts IssueCreateOptions) (Issue, error)
	CreateIssueLink(ctx context.Context, opts IssueLinkCreateOptions) error
}

type SplitSubtaskCreationService struct {
	Tracker      SplitSubtaskCreationTracker
	ReadyLabel   string
	SubtaskLabel string
}

type SplitSubtasksInput struct {
	QueueKey          string
	ParentID          string
	ParentTitle       string
	ParentDescription string
	Output            splitter.StrictOutput
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
			Description: buildSplitSubtaskBody(task, splitSubtaskContext(input, tasks, task)),
			Labels:      splitSubtaskLabels(s.effectiveReadyLabel(), s.effectiveSubtaskLabel()),
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

		for _, dependencyID := range splitDependencyIssueIDs(task.DependsOn, result.IssueIDsBySplitTaskID) {
			if err := s.Tracker.CreateIssueLink(ctx, IssueLinkCreateOptions{
				IssueID:        issueID,
				Relationship:   startrekDependsOnRelationship,
				RelatedIssueID: dependencyID,
			}); err != nil {
				return SplitSubtasksResult{}, fmt.Errorf("create startrek dependency link %q -> %q for split task %q: %w", issueID, dependencyID, taskID, err)
			}
		}
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

func (c *Client) CreateIssueLink(ctx context.Context, opts IssueLinkCreateOptions) error {
	issueID := strings.TrimSpace(opts.IssueID)
	if issueID == "" {
		return errors.New("startrek issue link source issue id is required")
	}
	relationship := strings.TrimSpace(opts.Relationship)
	if relationship == "" {
		return errors.New("startrek issue link relationship is required")
	}
	relatedIssueID := strings.TrimSpace(opts.RelatedIssueID)
	if relatedIssueID == "" {
		return errors.New("startrek issue link related issue id is required")
	}
	requestPath, err := issueLinksPath(issueID)
	if err != nil {
		return err
	}
	requestBody := startrekIssueLinkCreateRequest{
		Relationship: relationship,
		Issue:        relatedIssueID,
	}
	if err := c.DoJSON(ctx, http.MethodPost, requestPath, requestBody, nil); err != nil {
		return fmt.Errorf("create startrek issue link %q %s %q: %w", issueID, relationship, relatedIssueID, err)
	}
	return nil
}

func (b *StorageBackend) CreateIssue(ctx context.Context, opts IssueCreateOptions) (Issue, error) {
	if b == nil || b.client == nil {
		return Issue{}, errors.New("startrek storage backend is not initialized")
	}
	return b.client.CreateIssue(ctx, opts)
}

func (b *StorageBackend) CreateIssueLink(ctx context.Context, opts IssueLinkCreateOptions) error {
	if b == nil || b.client == nil {
		return errors.New("startrek storage backend is not initialized")
	}
	return b.client.CreateIssueLink(ctx, opts)
}

type startrekIssueCreateRequest struct {
	Queue       string   `json:"queue"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	MarkupType  string   `json:"markupType,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type startrekIssueLinkCreateRequest struct {
	Relationship string `json:"relationship"`
	Issue        string `json:"issue"`
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

type splitSubtaskBodyContext struct {
	EpicSummary       string
	DocLinks          []string
	ArtifactProducers []string
}

func splitSubtaskContext(input SplitSubtasksInput, tasks []splitter.Task, task splitter.Task) splitSubtaskBodyContext {
	return splitSubtaskBodyContext{
		EpicSummary:       splitEpicSummary(input),
		DocLinks:          splitDocLinks(input),
		ArtifactProducers: splitArtifactProducerPointers(tasks, task),
	}
}

func splitEpicSummary(input SplitSubtasksInput) string {
	parentID := strings.TrimSpace(input.ParentID)
	parentTitle := strings.TrimSpace(input.ParentTitle)
	parentDescription := splitParentEpicDescription(input.ParentDescription)
	summary := firstEpicSummarySentence(parentDescription)

	if parentTitle == "" && len(input.Output.Epics) > 0 {
		parentTitle = strings.TrimSpace(input.Output.Epics[0].Name)
	}
	if summary == "" && len(input.Output.Epics) > 0 {
		summary = strings.TrimSpace(input.Output.Epics[0].Goal)
	}

	head := strings.TrimSpace(strings.Join(nonEmptySplitContextItems(parentID, parentTitle), " "))
	switch {
	case head != "" && summary != "":
		return head + " - " + summary
	case head != "":
		return head
	case summary != "":
		return summary
	default:
		return "none"
	}
}

func firstEpicSummarySentence(description string) string {
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return firstSentence(line)
	}
	return ""
}

func splitParentEpicDescription(description string) string {
	if block, ok := splitMappedStartrekDescriptionBlock(description); ok {
		return block
	}
	return strings.TrimSpace(description)
}

func splitMappedStartrekDescriptionBlock(description string) (string, bool) {
	description = strings.ReplaceAll(description, "\r\n", "\n")
	description = strings.ReplaceAll(description, "\r", "\n")

	lines := strings.Split(description, "\n")
	descriptionIndex := -1
	fieldCount := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "Description:" {
			descriptionIndex = i
			break
		}
		if isMappedStartrekTaskField(line) {
			fieldCount++
		}
	}
	if descriptionIndex < 0 || fieldCount < 2 {
		return "", false
	}

	endIndex := len(lines)
	for i := descriptionIndex + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "Recent comments:" {
			endIndex = i
			break
		}
	}

	block := strings.TrimSpace(strings.Join(lines[descriptionIndex+1:endIndex], "\n"))
	if strings.EqualFold(block, "None") {
		return "", true
	}
	return block, true
}

func isMappedStartrekTaskField(line string) bool {
	name, _, ok := strings.Cut(line, ":")
	if !ok {
		return false
	}
	switch strings.TrimSpace(name) {
	case "Title", "Issue", "Queue", "Root", "Author", "Labels":
		return true
	default:
		return false
	}
}

func firstSentence(line string) string {
	best := -1
	for _, separator := range []string{". ", "? ", "! "} {
		if idx := strings.Index(line, separator); idx >= 0 && (best == -1 || idx < best) {
			best = idx
		}
	}
	if best >= 0 {
		return strings.TrimSpace(line[:best+1])
	}
	return line
}

func splitDocLinks(input SplitSubtasksInput) []string {
	values := []string{splitParentEpicDescription(input.ParentDescription)}
	for _, epic := range input.Output.Epics {
		values = append(values, epic.Name, epic.Goal)
	}

	links := make([]string, 0)
	seen := map[string]struct{}{}
	appendLink := func(link string) {
		link = strings.TrimRight(strings.TrimSpace(link), ".,;:!?")
		if link == "" {
			return
		}
		if _, ok := seen[link]; ok {
			return
		}
		seen[link] = struct{}{}
		links = append(links, link)
	}

	for _, value := range values {
		for _, match := range markdownDocLinkPattern.FindAllStringSubmatch(value, -1) {
			if len(match) > 1 {
				appendLink(match[1])
			}
		}
		for _, match := range bareDocLinkPattern.FindAllString(value, -1) {
			appendLink(match)
		}
	}
	return links
}

func splitArtifactProducerPointers(tasks []splitter.Task, current splitter.Task) []string {
	currentID := trimSplitRef(current.ID)
	producers := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskID := trimSplitRef(task.ID)
		if taskID == "" || taskID == currentID {
			continue
		}
		expectedFiles := normalizedSplitItems(task.ExpectedFiles)
		if len(expectedFiles) == 0 {
			continue
		}
		producers = append(producers, splitSubtaskTitle(task)+" -> "+strings.Join(expectedFiles, ", "))
	}
	return producers
}

func nonEmptySplitContextItems(items ...string) []string {
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			normalized = append(normalized, item)
		}
	}
	return normalized
}

func buildSplitSubtaskBody(task splitter.Task, context splitSubtaskBodyContext) string {
	var b strings.Builder
	b.WriteString("### Task: ")
	b.WriteString(splitSubtaskTitle(task))
	b.WriteString("\n\n")

	writeSplitContextSection(&b, context)
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

func writeSplitContextSection(b *strings.Builder, context splitSubtaskBodyContext) {
	b.WriteString("Context:\n")
	b.WriteString("- Epic summary: ")
	b.WriteString(fallbackText(context.EpicSummary, "none"))
	b.WriteByte('\n')

	docLinks := normalizedSplitItems(context.DocLinks)
	if len(docLinks) == 0 {
		b.WriteString("- Doc: none\n")
	} else {
		for _, link := range docLinks {
			b.WriteString("- Doc: ")
			b.WriteString(link)
			b.WriteByte('\n')
		}
	}

	producers := normalizedSplitItems(context.ArtifactProducers)
	if len(producers) == 0 {
		b.WriteString("- Artifact producer: none\n\n")
		return
	}
	for _, producer := range producers {
		b.WriteString("- Artifact producer: ")
		b.WriteString(producer)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
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

func splitSubtaskLabels(readyLabel string, subtaskLabel string) []string {
	labels := make([]string, 0, 2)
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
	return labels
}

func splitDependencyIssueIDs(dependsOn []string, createdIssueIDs map[string]string) []string {
	ids := make([]string, 0, len(dependsOn))
	seen := map[string]struct{}{}
	for _, dependency := range dependsOn {
		dependencyID := splitDependencyIssueID(dependency, createdIssueIDs)
		if dependencyID == "" {
			continue
		}
		key := strings.ToLower(dependencyID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ids = append(ids, dependencyID)
	}
	return ids
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
