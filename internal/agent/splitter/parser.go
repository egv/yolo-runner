package splitter

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type StrictOutput struct {
	Epics     []Epic
	Tasks     []Task
	Order     []Dependency
	RiskNotes []string
}

type Output = StrictOutput

type Epic struct {
	Name string
	Goal string
}

type Dependency struct {
	From string
	To   string
}

type Task struct {
	ID            string
	Title         string
	Why           []string
	InScope       []string
	OutOfScope    []string
	StrictTDD     []string
	DoneWhen      []string
	ExpectedFiles []string
	DependsOn     []string
	Unlocks       []string
}

func Parse(input string) (StrictOutput, error) {
	return ParseStrictOutput(input)
}

func ParseStrictOutput(input string) (StrictOutput, error) {
	lines := splitLines(input)

	epicLines, ok := markdownSection(lines, "Epics")
	if !ok {
		return StrictOutput{}, fmt.Errorf("missing Epics section")
	}
	epics, err := parseEpics(epicLines)
	if err != nil {
		return StrictOutput{}, err
	}

	taskLines, ok := markdownSection(lines, "Tasks")
	if !ok {
		return StrictOutput{}, fmt.Errorf("missing Tasks section")
	}
	tasks, err := parseTaskSummaries(taskLines)
	if err != nil {
		if !errors.Is(err, errNoTaskSummaries) {
			return StrictOutput{}, err
		}
		tasks, err = parseTaskSummariesFromTemplates(lines)
		if err != nil {
			return StrictOutput{}, err
		}
	}

	orderLines, ok := markdownSection(lines, "Order")
	if !ok {
		return StrictOutput{}, fmt.Errorf("missing Order section")
	}
	order, err := parseOrder(orderLines)
	if err != nil {
		return StrictOutput{}, err
	}

	riskLines, ok := markdownSection(lines, "Risk notes")
	if !ok {
		return StrictOutput{}, fmt.Errorf("missing Risk notes section")
	}
	riskItems := cleanListItems(riskLines)
	if len(riskItems) == 0 {
		return StrictOutput{}, fmt.Errorf("Risk notes section must contain at least one item or none")
	}
	riskNotes := dropNone(riskItems)

	taskTitleByID := make(map[string]string, len(tasks))
	taskIDByTitle := make(map[string]string, len(tasks))
	for _, task := range tasks {
		taskTitleByID[task.ID] = task.Title
		taskIDByTitle[task.Title] = task.ID
	}

	templates, err := parseStrictTaskTemplates(lines, taskTitleByID, taskIDByTitle)
	if err != nil {
		return StrictOutput{}, err
	}

	for i, task := range tasks {
		template, ok := templates[task.ID]
		if !ok {
			return StrictOutput{}, fmt.Errorf("task %s missing strict task template", task.ID)
		}
		if template.Title == "" {
			template.Title = task.Title
		}
		tasks[i] = template
		delete(templates, task.ID)
	}
	for id := range templates {
		return StrictOutput{}, fmt.Errorf("strict task template %s is not listed in Tasks section", id)
	}

	return StrictOutput{
		Epics:     epics,
		Tasks:     tasks,
		Order:     order,
		RiskNotes: riskNotes,
	}, nil
}

func (o StrictOutput) TaskByID(id string) *Task {
	for i := range o.Tasks {
		if o.Tasks[i].ID == id {
			return &o.Tasks[i]
		}
	}
	return nil
}

var (
	errNoTaskSummaries = errors.New("Tasks section must contain at least one task")
	numberedListItemRE = regexp.MustCompile(`^\d+[.)]\s+(.+)$`)
	simpleRefRE        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	ambiguousWordRE    = regexp.MustCompile(`(?i)\b(and|or)\b`)
)

func splitLines(input string) []string {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}

func markdownSection(lines []string, name string) ([]string, bool) {
	want := canonicalLabel(name)
	for i, line := range lines {
		level, title, ok := markdownHeading(line)
		if !ok || level != 2 || canonicalLabel(title) != want {
			continue
		}

		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			nextLevel, _, nextOK := markdownHeading(lines[j])
			if nextOK && (nextLevel <= 2 || isTaskHeading(lines[j])) {
				end = j
				break
			}
		}
		return lines[i+1 : end], true
	}
	return nil, false
}

func markdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}

	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(trimmed[level+1:]), true
}

func isTaskHeading(line string) bool {
	level, title, ok := markdownHeading(line)
	return ok && level == 3 && strings.HasPrefix(canonicalLabel(title), "task:")
}

func canonicalLabel(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(value, ":")))
}

func cleanListItems(lines []string) []string {
	var items []string
	for _, line := range lines {
		item := strings.TrimSpace(line)
		if item == "" || strings.HasPrefix(item, "```") {
			continue
		}
		if strings.HasPrefix(item, "- ") || strings.HasPrefix(item, "* ") {
			item = strings.TrimSpace(item[2:])
		} else if match := numberedListItemRE.FindStringSubmatch(item); match != nil {
			item = strings.TrimSpace(match[1])
		}
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseEpics(lines []string) ([]Epic, error) {
	items := dropNone(cleanListItems(lines))
	if len(items) == 0 {
		return nil, fmt.Errorf("Epics section must contain at least one epic")
	}

	epics := make([]Epic, 0, len(items))
	for _, item := range items {
		name, goal, ok := strings.Cut(item, ":")
		if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(goal) == "" {
			return nil, fmt.Errorf("invalid Epics item %q: expected \"name: goal\"", item)
		}
		epics = append(epics, Epic{
			Name: strings.TrimSpace(name),
			Goal: strings.TrimSpace(goal),
		})
	}
	return epics, nil
}

func parseTaskSummaries(lines []string) ([]Task, error) {
	items := dropNone(cleanListItems(lines))
	if len(items) == 0 {
		return nil, errNoTaskSummaries
	}

	tasks := make([]Task, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id, title, err := splitTaskSummary(item)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate task %s in Tasks section", id)
		}
		seen[id] = true
		tasks = append(tasks, Task{ID: id, Title: title})
	}
	return tasks, nil
}

func parseTaskSummariesFromTemplates(lines []string) ([]Task, error) {
	var tasks []Task
	seen := make(map[string]bool)
	for _, line := range lines {
		if !isTaskHeading(line) {
			continue
		}
		_, heading, _ := markdownHeading(line)
		raw := stripTaskHeadingPrefix(heading)
		id, title, err := splitTaskTemplateHeadingSummary(raw)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate task %s in task templates", id)
		}
		seen[id] = true
		tasks = append(tasks, Task{ID: id, Title: title})
	}
	if len(tasks) == 0 {
		return nil, errNoTaskSummaries
	}
	return tasks, nil
}

func splitTaskTemplateHeadingSummary(raw string) (string, string, error) {
	fields := strings.Fields(raw)
	if len(fields) < 2 {
		return "", "", fmt.Errorf("invalid task template heading %q: expected \"id title\"", raw)
	}
	id := strings.TrimSuffix(trimRef(fields[0]), ":")
	title := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))
	title = strings.TrimLeft(title, ":- ")
	return validateTaskSummary(raw, id, title)
}

func splitTaskSummary(item string) (string, string, error) {
	if id, title, ok := strings.Cut(item, ":"); ok {
		return validateTaskSummary(item, id, title)
	}
	if id, title, ok := strings.Cut(item, " - "); ok {
		return validateTaskSummary(item, id, title)
	}

	fields := strings.Fields(item)
	if len(fields) < 2 {
		return "", "", fmt.Errorf("invalid Tasks item %q: expected \"id: title\"", item)
	}
	return validateTaskSummary(item, fields[0], strings.TrimSpace(strings.TrimPrefix(item, fields[0])))
}

func validateTaskSummary(original, id, title string) (string, string, error) {
	id = trimRef(id)
	title = strings.TrimSpace(title)
	if id == "" || title == "" {
		return "", "", fmt.Errorf("invalid Tasks item %q: expected \"id: title\"", original)
	}
	if !simpleRefRE.MatchString(id) {
		return "", "", fmt.Errorf("invalid task id %q in Tasks section", id)
	}
	return id, title, nil
}

func parseOrder(lines []string) ([]Dependency, error) {
	items := cleanListItems(lines)
	if len(items) == 0 {
		return nil, fmt.Errorf("Order section must contain at least one dependency chain or none")
	}
	items = dropNone(items)
	if len(items) == 0 {
		return nil, nil
	}

	var dependencies []Dependency
	for _, item := range items {
		if isReadyOrderAnnotation(item) {
			continue
		}
		blockedDeps, ok, err := parseBlockedByOrderAnnotation(item)
		if err != nil {
			return nil, err
		}
		if ok {
			dependencies = append(dependencies, blockedDeps...)
			continue
		}

		if ambiguousOrder(item) {
			return nil, fmt.Errorf("Order contains ambiguous dependency chain %q", item)
		}

		parts := strings.Split(item, "->")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid Order item %q: expected arrow chain", item)
		}

		refs := make([]string, 0, len(parts))
		for _, part := range parts {
			ref := trimRef(part)
			if ref == "" || !simpleRefRE.MatchString(ref) {
				return nil, fmt.Errorf("invalid Order dependency reference %q in %q", part, item)
			}
			refs = append(refs, ref)
		}

		for i := 0; i < len(refs)-1; i++ {
			dependencies = append(dependencies, Dependency{From: refs[i], To: refs[i+1]})
		}
	}
	return dependencies, nil
}

func isReadyOrderAnnotation(item string) bool {
	normalized := strings.ToLower(strings.TrimSpace(item))
	for _, prefix := range []string{
		"ready:",
		"ready now:",
		"ready task:",
		"ready tasks:",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func parseBlockedByOrderAnnotation(item string) ([]Dependency, bool, error) {
	const prefix = "blocked by "
	trimmed := strings.TrimSpace(item)
	normalized := strings.ToLower(trimmed)
	if !strings.HasPrefix(normalized, prefix) {
		return nil, false, nil
	}

	rest := strings.TrimSpace(trimmed[len(prefix):])
	fromPart, toPart, ok := strings.Cut(rest, ":")
	if !ok {
		return nil, true, fmt.Errorf("invalid Order item %q: expected \"Blocked by <task>: <task>\"", item)
	}

	from := trimRef(fromPart)
	if from == "" || !simpleRefRE.MatchString(from) {
		return nil, true, fmt.Errorf("invalid Order blocked-by reference %q in %q", fromPart, item)
	}

	var deps []Dependency
	for _, part := range strings.Split(toPart, ",") {
		to := trimRef(part)
		if to == "" {
			continue
		}
		if ambiguousWordRE.MatchString(to) || strings.ContainsAny(to, "/&") || !simpleRefRE.MatchString(to) {
			return nil, true, fmt.Errorf("invalid Order blocked task reference %q in %q", part, item)
		}
		deps = append(deps, Dependency{From: from, To: to})
	}
	if len(deps) == 0 {
		return nil, true, fmt.Errorf("invalid Order item %q: expected at least one blocked task", item)
	}
	return deps, true, nil
}

func ambiguousOrder(item string) bool {
	return ambiguousWordRE.MatchString(item) || strings.ContainsAny(item, "/&,")
}

func parseStrictTaskTemplates(lines []string, taskTitleByID map[string]string, taskIDByTitle map[string]string) (map[string]Task, error) {
	templates := make(map[string]Task)
	for i := 0; i < len(lines); i++ {
		if !isTaskHeading(lines[i]) {
			continue
		}

		_, heading, _ := markdownHeading(lines[i])
		rawTitle := stripTaskHeadingPrefix(heading)
		id, title, err := splitTaskHeading(rawTitle, taskTitleByID, taskIDByTitle)
		if err != nil {
			return nil, err
		}
		if _, exists := templates[id]; exists {
			return nil, fmt.Errorf("duplicate strict task template for %s", id)
		}

		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			nextLevel, _, nextOK := markdownHeading(lines[j])
			if isTaskHeading(lines[j]) || (nextOK && nextLevel <= 2) {
				end = j
				break
			}
		}

		task, err := parseStrictTaskBlock(id, title, lines[i+1:end])
		if err != nil {
			return nil, err
		}
		templates[id] = task
		i = end - 1
	}
	return templates, nil
}

func stripTaskHeadingPrefix(heading string) string {
	trimmed := strings.TrimSpace(heading)
	if strings.HasPrefix(canonicalLabel(trimmed), "task:") {
		return strings.TrimSpace(trimmed[len("Task:"):])
	}
	return trimmed
}

func splitTaskHeading(raw string, taskTitleByID map[string]string, taskIDByTitle map[string]string) (string, string, error) {
	fields := strings.Fields(raw)
	if len(fields) > 0 {
		candidate := strings.TrimSuffix(trimRef(fields[0]), ":")
		if title, ok := taskTitleByID[candidate]; ok {
			remaining := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))
			remaining = strings.TrimLeft(remaining, ":- ")
			if remaining == "" {
				remaining = title
			}
			return candidate, remaining, nil
		}
	}

	if id, ok := taskIDByTitle[raw]; ok {
		return id, raw, nil
	}

	return "", "", fmt.Errorf("strict task template %q is not listed in Tasks section", raw)
}

type templateSection struct {
	label string
	key   string
}

var strictTemplateSections = []templateSection{
	{label: "Why", key: "why"},
	{label: "In scope", key: "in scope"},
	{label: "Out of scope", key: "out of scope"},
	{label: "Strict TDD", key: "strict tdd"},
	{label: "Done when", key: "done when"},
	{label: "Expected files", key: "expected files"},
	{label: "Depends on", key: "depends on"},
	{label: "Unlocks", key: "unlocks"},
}

func parseStrictTaskBlock(id, title string, lines []string) (Task, error) {
	sectionLines := make(map[string][]string)
	seen := make(map[string]bool)
	current := ""

	for _, line := range lines {
		key, ok := strictTemplateSectionKey(line)
		if ok {
			current = key
			seen[key] = true
			continue
		}
		if current != "" {
			sectionLines[current] = append(sectionLines[current], line)
		}
	}

	cleaned := make(map[string][]string, len(strictTemplateSections))
	for _, section := range strictTemplateSections {
		if !seen[section.key] {
			return Task{}, fmt.Errorf("task %s missing strict section %q", id, section.label)
		}
		items := cleanListItems(sectionLines[section.key])
		if len(items) == 0 {
			return Task{}, fmt.Errorf("task %s strict section %q must contain at least one item", id, section.label)
		}
		cleaned[section.key] = items
	}

	dependsOn, err := parseTemplateRefs(id, "Depends on", cleaned["depends on"])
	if err != nil {
		return Task{}, err
	}
	unlocks, err := parseTemplateRefs(id, "Unlocks", cleaned["unlocks"])
	if err != nil {
		return Task{}, err
	}

	return Task{
		ID:            id,
		Title:         title,
		Why:           cleaned["why"],
		InScope:       cleaned["in scope"],
		OutOfScope:    cleaned["out of scope"],
		StrictTDD:     cleaned["strict tdd"],
		DoneWhen:      cleaned["done when"],
		ExpectedFiles: cleaned["expected files"],
		DependsOn:     dependsOn,
		Unlocks:       unlocks,
	}, nil
}

func strictTemplateSectionKey(line string) (string, bool) {
	key := canonicalLabel(line)
	for _, section := range strictTemplateSections {
		if key == section.key {
			return section.key, true
		}
	}
	return "", false
}

func parseTemplateRefs(taskID, section string, items []string) ([]string, error) {
	var refs []string
	for _, item := range items {
		for _, part := range strings.Split(item, ",") {
			ref := trimRef(part)
			if isNone(ref) {
				continue
			}
			if ambiguousWordRE.MatchString(ref) || strings.ContainsAny(ref, "/&") {
				return nil, fmt.Errorf("task %s section %q contains ambiguous dependency reference %q", taskID, section, item)
			}
			if !simpleRefRE.MatchString(ref) {
				return nil, fmt.Errorf("task %s section %q contains invalid dependency reference %q", taskID, section, ref)
			}
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func dropNone(items []string) []string {
	filtered := items[:0]
	for _, item := range items {
		if !isNone(item) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func isNone(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "n/a", "na":
		return true
	default:
		return false
	}
}

func trimRef(value string) string {
	return strings.Trim(strings.TrimSpace(value), "`")
}
