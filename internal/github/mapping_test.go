package github

import (
	"reflect"
	"testing"
)

func TestMapToTaskGraphMapsRootAndIssueParents(t *testing.T) {
	issues := []Issue{
		{Number: 100, Title: "Roadmap", Body: "Root issue body", Labels: []string{"p1"}},
		{Number: 101, Title: "Top level task", Labels: []string{"priority:3"}},
		{Number: 102, Title: "Another task"},
	}

	graph, err := MapToTaskGraph(100, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	if graph.RootID != "100" {
		t.Fatalf("expected root ID 100, got %q", graph.RootID)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(graph.Nodes))
	}

	root, ok := graph.Nodes["100"]
	if !ok {
		t.Fatalf("expected root node to exist")
	}
	if root.Kind != NodeKindRoot {
		t.Fatalf("expected root kind %q, got %q", NodeKindRoot, root.Kind)
	}
	if root.ParentID != "" {
		t.Fatalf("expected root parent to be empty, got %q", root.ParentID)
	}
	if root.Priority != 1 {
		t.Fatalf("expected root priority 1 from p1 label, got %d", root.Priority)
	}

	if got := graph.Nodes["101"].ParentID; got != "100" {
		t.Fatalf("expected issue 101 parent 100, got %q", got)
	}
	if got := graph.Nodes["102"].ParentID; got != "100" {
		t.Fatalf("expected issue 102 parent 100, got %q", got)
	}
}

func TestMapToTaskGraphCreatesSyntheticRootWhenMissing(t *testing.T) {
	issues := []Issue{
		{Number: 501, Title: "Only child"},
	}

	graph, err := MapToTaskGraph(500, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	if graph.RootID != "500" {
		t.Fatalf("expected root ID 500, got %q", graph.RootID)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes (synthetic root + issue), got %d", len(graph.Nodes))
	}

	root := graph.Nodes["500"]
	if root.Kind != NodeKindRoot {
		t.Fatalf("expected root kind %q, got %q", NodeKindRoot, root.Kind)
	}
	if root.Title != "#500" {
		t.Fatalf("expected synthetic root title #500, got %q", root.Title)
	}
	if got := graph.Nodes["501"].ParentID; got != "500" {
		t.Fatalf("expected issue 501 parent 500, got %q", got)
	}
}

func TestMapToTaskGraphNormalizesDependenciesFromLabelsAndBody(t *testing.T) {
	issues := []Issue{
		{Number: 100, Title: "Root"},
		{
			Number: 101,
			Title:  "Task with dependencies",
			Labels: []string{
				"depends-on:#102",
				"blocked-by: #103, #999, #101",
				"Depends On: 104",
			},
			Body: `
Depends on #104
- blocked by https://github.com/acme/repo/issues/103
* depends_on: acme/repo#102
`,
		},
		{Number: 102, Title: "Dep A"},
		{Number: 103, Title: "Dep B"},
		{Number: 104, Title: "Dep C"},
	}

	graph, err := MapToTaskGraph(100, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	want := []string{"102", "103", "104"}
	if got := graph.Nodes["101"].Dependencies; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected normalized dependencies %v, got %v", want, got)
	}
}

func TestNormalizePriorityParsesGitHubLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   int
	}{
		{name: "p0", labels: []string{"p0"}, want: 0},
		{name: "priority label", labels: []string{"priority:3"}, want: 3},
		{name: "case and spacing", labels: []string{"Priority : 1"}, want: 1},
		{name: "multiple picks lowest", labels: []string{"p3", "priority:1", "p2"}, want: 1},
		{name: "out of range ignored", labels: []string{"p9"}, want: defaultPriority},
		{name: "missing defaults", labels: nil, want: defaultPriority},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePriority(tc.labels); got != tc.want {
				t.Fatalf("NormalizePriority(%v) = %d, want %d", tc.labels, got, tc.want)
			}
		})
	}
}

func TestTaskGraphChildrenOfSortByPriorityThenIssueNumber(t *testing.T) {
	issues := []Issue{
		{Number: 100, Title: "Root"},
		{Number: 30, Title: "C", Labels: []string{"p3"}},
		{Number: 12, Title: "B", Labels: []string{"p1"}},
		{Number: 2, Title: "A", Labels: []string{"priority:1"}},
	}

	graph, err := MapToTaskGraph(100, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	children := graph.ChildrenOf("100")
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	got := []string{children[0].ID, children[1].ID, children[2].ID}
	want := []string{"2", "12", "30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected child order %v, got %v", want, got)
	}
}

func TestMapToTaskGraphRejectsDuplicateIssueNumbers(t *testing.T) {
	issues := []Issue{
		{Number: 100, Title: "Root"},
		{Number: 101, Title: "Task A"},
		{Number: 101, Title: "Task B"},
	}

	if _, err := MapToTaskGraph(100, issues); err == nil {
		t.Fatalf("expected duplicate issue numbers to fail")
	}
}

func TestMapToTaskGraphRejectsInvalidRootAndIssueNumbers(t *testing.T) {
	if _, err := MapToTaskGraph(0, nil); err == nil {
		t.Fatalf("expected invalid root number to fail")
	}

	issues := []Issue{
		{Number: 100, Title: "Root"},
		{Number: -1, Title: "Invalid"},
	}
	if _, err := MapToTaskGraph(100, issues); err == nil {
		t.Fatalf("expected invalid issue number to fail")
	}
}
