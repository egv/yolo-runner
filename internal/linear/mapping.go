package linear

import (
	"fmt"
	"sort"
	"strings"
)

// Project is the minimal Linear project payload required for graph mapping.
type Project struct {
	ID   string
	Name string
}

// Issue is the minimal Linear issue payload required for graph mapping.
type Issue struct {
	ID            string
	ProjectID     string
	ParentIssueID string
	BlockedByIDs  []string
	Title         string
	Description   string
	Priority      int
}

type NodeKind string

const (
	NodeKindProject NodeKind = "project"
	NodeKindIssue   NodeKind = "issue"
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
// Linear adapter.
type TaskGraph struct {
	RootID string
	Nodes  map[string]Node
}

// MapToTaskGraph maps a Linear project and its issues into a deterministic
// task graph model.
func MapToTaskGraph(project Project, issues []Issue) (TaskGraph, error) {
	projectID := strings.TrimSpace(project.ID)
	if projectID == "" {
		return TaskGraph{}, fmt.Errorf("project ID is required")
	}

	inScope, issueIDs, err := collectInScopeIssues(projectID, issues)
	if err != nil {
		return TaskGraph{}, err
	}

	nodes := map[string]Node{
		projectID: {
			ID:       projectID,
			Kind:     NodeKindProject,
			Title:    fallbackText(project.Name, projectID),
			Priority: 0,
		},
	}

	for _, issue := range inScope {
		parentID := projectID
		parentCandidate := strings.TrimSpace(issue.ParentIssueID)
		if _, ok := issueIDs[parentCandidate]; ok {
			parentID = parentCandidate
		}
		nodes[issue.ID] = Node{
			ID:           issue.ID,
			Kind:         NodeKindIssue,
			Title:        fallbackText(issue.Title, issue.ID),
			Description:  issue.Description,
			ParentID:     parentID,
			Priority:     NormalizePriority(issue.Priority),
			Dependencies: normalizeDependencies(issue.ID, issue.BlockedByIDs, issueIDs),
		}
	}

	return TaskGraph{
		RootID: projectID,
		Nodes:  nodes,
	}, nil
}

// NormalizePriority converts Linear priorities to scheduler-friendly ordering
// where lower is more urgent.
func NormalizePriority(raw int) int {
	switch raw {
	case 1:
		return 0
	case 2:
		return 1
	case 3:
		return 2
	case 4:
		return 3
	default:
		return defaultPriority
	}
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
			return children[i].ID < children[j].ID
		}
		return children[i].Priority < children[j].Priority
	})
	return children
}

func collectInScopeIssues(projectID string, issues []Issue) ([]Issue, map[string]struct{}, error) {
	inScope := make([]Issue, 0, len(issues))
	issueIDs := make(map[string]struct{}, len(issues))

	for _, issue := range issues {
		id := strings.TrimSpace(issue.ID)
		if id == "" {
			return nil, nil, fmt.Errorf("issue ID is required")
		}
		issue.ID = id

		issueProjectID := strings.TrimSpace(issue.ProjectID)
		if issueProjectID != "" && issueProjectID != projectID {
			continue
		}
		if _, exists := issueIDs[id]; exists {
			return nil, nil, fmt.Errorf("duplicate issue ID %q", id)
		}
		issueIDs[id] = struct{}{}
		inScope = append(inScope, issue)
	}

	return inScope, issueIDs, nil
}

func normalizeDependencies(issueID string, blockedBy []string, issueIDs map[string]struct{}) []string {
	if len(blockedBy) == 0 {
		return nil
	}

	unique := map[string]struct{}{}
	for _, depID := range blockedBy {
		depID = strings.TrimSpace(depID)
		if depID == "" || depID == issueID {
			continue
		}
		if _, ok := issueIDs[depID]; !ok {
			continue
		}
		unique[depID] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}

	deps := make([]string, 0, len(unique))
	for depID := range unique {
		deps = append(deps, depID)
	}
	sort.Strings(deps)
	return deps
}

func fallbackText(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
