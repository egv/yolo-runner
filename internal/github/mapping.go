package github

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Issue is the minimal GitHub issue payload required for graph mapping.
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

type NodeKind string

const (
	NodeKindRoot  NodeKind = "root"
	NodeKindIssue NodeKind = "issue"
)

const defaultPriority = 2

// Node is a mapped task-graph node.
type Node struct {
	ID           string
	Kind         NodeKind
	Title        string
	Description  string
	ParentID     string
	Priority     int
	Dependencies []string
}

// TaskGraph is the normalized parent/dependency/priority view used by the
// GitHub adapter.
type TaskGraph struct {
	RootID string
	Nodes  map[string]Node
}

var (
	dependencyLabelPattern         = regexp.MustCompile(`(?i)^(depends[-_ ]?on|blocked[-_ ]?by)\s*:\s*(.+)$`)
	dependencyBodyDirectivePattern = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?(?:depends[-_ ]?on|blocked[-_ ]?by)\s*[:\-]?\s*(.+)$`)
	hashIssueRefPattern            = regexp.MustCompile(`(?i)(?:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)?#([0-9]+)\b`)
	urlIssueRefPattern             = regexp.MustCompile(`(?i)/issues/([0-9]+)\b`)
	priorityPLabelPattern          = regexp.MustCompile(`(?i)^p([0-4])$`)
	priorityLabelPattern           = regexp.MustCompile(`(?i)^priority(?:\s*[:=\-]\s*|\s+)([0-4])$`)
)

// MapToTaskGraph maps a root issue number and related GitHub issues into a
// deterministic task graph model.
func MapToTaskGraph(rootNumber int, issues []Issue) (TaskGraph, error) {
	if rootNumber <= 0 {
		return TaskGraph{}, fmt.Errorf("root issue number must be positive")
	}
	rootID := issueID(rootNumber)

	inScope, issueIDs, err := collectInScopeIssues(issues)
	if err != nil {
		return TaskGraph{}, err
	}

	validIDs := make(map[string]struct{}, len(issueIDs)+1)
	for issueID := range issueIDs {
		validIDs[issueID] = struct{}{}
	}
	validIDs[rootID] = struct{}{}

	nodes := make(map[string]Node, len(inScope)+1)
	rootIssue, hasRootIssue := issueByNumber(rootNumber, inScope)
	if hasRootIssue {
		nodes[rootID] = buildNode(rootIssue, NodeKindRoot, "", validIDs)
	} else {
		nodes[rootID] = Node{
			ID:       rootID,
			Kind:     NodeKindRoot,
			Title:    "#" + rootID,
			Priority: 0,
		}
	}

	for _, issue := range inScope {
		nodeID := issueID(issue.Number)
		if nodeID == rootID {
			continue
		}
		nodes[nodeID] = buildNode(issue, NodeKindIssue, rootID, validIDs)
	}

	return TaskGraph{
		RootID: rootID,
		Nodes:  nodes,
	}, nil
}

// NormalizePriority extracts scheduler priority from GitHub labels.
// Supported conventions:
// - p0..p4
// - priority:0..4 (also accepts "priority 0", "priority=0", "priority-0")
func NormalizePriority(labels []string) int {
	best := defaultPriority + 10
	for _, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		if matches := priorityPLabelPattern.FindStringSubmatch(label); len(matches) == 2 {
			if value, ok := parsePriorityValue(matches[1]); ok && value < best {
				best = value
			}
			continue
		}
		if matches := priorityLabelPattern.FindStringSubmatch(label); len(matches) == 2 {
			if value, ok := parsePriorityValue(matches[1]); ok && value < best {
				best = value
			}
		}
	}
	if best > 4 {
		return defaultPriority
	}
	return best
}

// ChildrenOf returns child issues sorted deterministically by priority then ID.
func (g TaskGraph) ChildrenOf(parentID string) []Node {
	children := []Node{}
	for _, node := range g.Nodes {
		if node.ParentID == parentID {
			children = append(children, node)
		}
	}
	sort.Slice(children, func(i int, j int) bool {
		if children[i].Priority == children[j].Priority {
			return compareIssueIDs(children[i].ID, children[j].ID) < 0
		}
		return children[i].Priority < children[j].Priority
	})
	return children
}

func collectInScopeIssues(issues []Issue) ([]Issue, map[string]struct{}, error) {
	inScope := make([]Issue, 0, len(issues))
	issueIDs := make(map[string]struct{}, len(issues))

	for _, issue := range issues {
		if issue.Number <= 0 {
			return nil, nil, fmt.Errorf("issue number must be positive")
		}
		issueID := issueID(issue.Number)
		if _, exists := issueIDs[issueID]; exists {
			return nil, nil, fmt.Errorf("duplicate issue number %d", issue.Number)
		}
		issueIDs[issueID] = struct{}{}
		inScope = append(inScope, issue)
	}

	return inScope, issueIDs, nil
}

func issueByNumber(number int, issues []Issue) (Issue, bool) {
	for _, issue := range issues {
		if issue.Number == number {
			return issue, true
		}
	}
	return Issue{}, false
}

func buildNode(issue Issue, kind NodeKind, parentID string, validIDs map[string]struct{}) Node {
	nodeID := issueID(issue.Number)
	return Node{
		ID:           nodeID,
		Kind:         kind,
		Title:        fallbackText(issue.Title, "#"+nodeID),
		Description:  issue.Body,
		ParentID:     parentID,
		Priority:     NormalizePriority(issue.Labels),
		Dependencies: normalizeDependencies(nodeID, dependencyReferences(issue), validIDs),
	}
}

func normalizeDependencies(issueID string, references []int, validIDs map[string]struct{}) []string {
	if len(references) == 0 {
		return nil
	}

	unique := map[string]struct{}{}
	for _, ref := range references {
		dependencyID := issueIDFromNumber(ref)
		if dependencyID == issueID {
			continue
		}
		if _, ok := validIDs[dependencyID]; !ok {
			continue
		}
		unique[dependencyID] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}

	deps := make([]string, 0, len(unique))
	for depID := range unique {
		deps = append(deps, depID)
	}
	sort.Slice(deps, func(i int, j int) bool {
		return compareIssueIDs(deps[i], deps[j]) < 0
	})
	return deps
}

func dependencyReferences(issue Issue) []int {
	if len(issue.Labels) == 0 && strings.TrimSpace(issue.Body) == "" {
		return nil
	}

	unique := map[int]struct{}{}

	for _, rawLabel := range issue.Labels {
		matches := dependencyLabelPattern.FindStringSubmatch(strings.TrimSpace(rawLabel))
		if len(matches) != 3 {
			continue
		}
		for _, ref := range extractIssueReferences(matches[2], true) {
			unique[ref] = struct{}{}
		}
	}

	for _, line := range strings.Split(issue.Body, "\n") {
		matches := dependencyBodyDirectivePattern.FindStringSubmatch(line)
		if len(matches) != 2 {
			continue
		}
		for _, ref := range extractIssueReferences(matches[1], true) {
			unique[ref] = struct{}{}
		}
	}

	if len(unique) == 0 {
		return nil
	}

	references := make([]int, 0, len(unique))
	for ref := range unique {
		references = append(references, ref)
	}
	sort.Ints(references)
	return references
}

func extractIssueReferences(raw string, allowBareNumbers bool) []int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	unique := map[int]struct{}{}
	for _, matches := range hashIssueRefPattern.FindAllStringSubmatch(raw, -1) {
		if len(matches) != 2 {
			continue
		}
		if issueNumber, ok := parsePositiveInt(matches[1]); ok {
			unique[issueNumber] = struct{}{}
		}
	}
	for _, matches := range urlIssueRefPattern.FindAllStringSubmatch(raw, -1) {
		if len(matches) != 2 {
			continue
		}
		if issueNumber, ok := parsePositiveInt(matches[1]); ok {
			unique[issueNumber] = struct{}{}
		}
	}

	if allowBareNumbers {
		for _, token := range bareReferenceTokens(raw) {
			if issueNumber, ok := parsePositiveInt(token); ok {
				unique[issueNumber] = struct{}{}
			}
		}
	}

	if len(unique) == 0 {
		return nil
	}

	references := make([]int, 0, len(unique))
	for ref := range unique {
		references = append(references, ref)
	}
	sort.Ints(references)
	return references
}

func bareReferenceTokens(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';' || r == '|'
	})
	if len(fields) == 0 {
		return nil
	}

	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.TrimSpace(field)
		token = strings.Trim(token, "[](){}<>.,;:")
		token = strings.TrimPrefix(token, "#")
		if token == "" {
			continue
		}
		if strings.Contains(token, "#") {
			parts := strings.Split(token, "#")
			token = parts[len(parts)-1]
		}
		if idx := strings.LastIndex(strings.ToLower(token), "/issues/"); idx >= 0 {
			token = token[idx+len("/issues/"):]
		}
		tokens = append(tokens, strings.TrimSpace(token))
	}
	return tokens
}

func parsePositiveInt(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

func parsePriorityValue(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 || value > 4 {
		return 0, false
	}
	return value, true
}

func compareIssueIDs(a string, b string) int {
	ai, aok := parsePositiveInt(a)
	bi, bok := parsePositiveInt(b)
	if aok && bok {
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func issueID(number int) string {
	return strconv.Itoa(number)
}

func issueIDFromNumber(number int) string {
	return strconv.Itoa(number)
}

func fallbackText(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
