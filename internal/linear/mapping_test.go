package linear

import (
	"reflect"
	"testing"
)

func TestMapToTaskGraphMapsProjectAndIssueParents(t *testing.T) {
	project := Project{ID: "proj-1", Name: "Roadmap"}
	issues := []Issue{
		{ID: "iss-1", ProjectID: "proj-1", Title: "Top level", Priority: 2},
		{ID: "iss-2", ProjectID: "proj-1", Title: "Child", ParentIssueID: "iss-1", Priority: 1},
		{ID: "iss-3", ProjectID: "proj-1", Title: "Missing parent falls back", ParentIssueID: "unknown", Priority: 3},
		{ID: "iss-x", ProjectID: "proj-2", Title: "Out of scope", Priority: 1},
	}

	graph, err := MapToTaskGraph(project, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	if graph.RootID != "proj-1" {
		t.Fatalf("expected root ID proj-1, got %q", graph.RootID)
	}
	if len(graph.Nodes) != 4 {
		t.Fatalf("expected 4 nodes (project + 3 issues), got %d", len(graph.Nodes))
	}

	projectNode, ok := graph.Nodes["proj-1"]
	if !ok {
		t.Fatalf("expected project node to exist")
	}
	if projectNode.Kind != NodeKindProject {
		t.Fatalf("expected project kind %q, got %q", NodeKindProject, projectNode.Kind)
	}

	if got := graph.Nodes["iss-1"].ParentID; got != "proj-1" {
		t.Fatalf("expected iss-1 parent proj-1, got %q", got)
	}
	if got := graph.Nodes["iss-2"].ParentID; got != "iss-1" {
		t.Fatalf("expected iss-2 parent iss-1, got %q", got)
	}
	if got := graph.Nodes["iss-3"].ParentID; got != "proj-1" {
		t.Fatalf("expected iss-3 parent fallback proj-1, got %q", got)
	}
	if _, exists := graph.Nodes["iss-x"]; exists {
		t.Fatalf("did not expect out-of-scope issue to be included")
	}
}

func TestMapToTaskGraphNormalizesDependencies(t *testing.T) {
	project := Project{ID: "proj-1", Name: "Roadmap"}
	issues := []Issue{
		{ID: "iss-1", ProjectID: "proj-1", BlockedByIDs: []string{"iss-2", "iss-1", "missing", "iss-3", "iss-2", "  iss-3  "}},
		{ID: "iss-2", ProjectID: "proj-1"},
		{ID: "iss-3", ProjectID: "proj-1"},
	}

	graph, err := MapToTaskGraph(project, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	want := []string{"iss-2", "iss-3"}
	if got := graph.Nodes["iss-1"].Dependencies; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected normalized dependencies %v, got %v", want, got)
	}
}

func TestNormalizePriorityMapsLinearScale(t *testing.T) {
	tests := []struct {
		name string
		raw  int
		want int
	}{
		{name: "urgent", raw: 1, want: 0},
		{name: "high", raw: 2, want: 1},
		{name: "normal", raw: 3, want: 2},
		{name: "low", raw: 4, want: 3},
		{name: "none defaults to normal", raw: 0, want: 2},
		{name: "negative defaults to normal", raw: -1, want: 2},
		{name: "out of range defaults to normal", raw: 9, want: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePriority(tc.raw); got != tc.want {
				t.Fatalf("NormalizePriority(%d) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestTaskGraphChildrenOfSortByPriorityThenID(t *testing.T) {
	project := Project{ID: "proj-1", Name: "Roadmap"}
	issues := []Issue{
		{ID: "iss-b", ProjectID: "proj-1", Priority: 4},
		{ID: "iss-c", ProjectID: "proj-1", Priority: 2},
		{ID: "iss-a", ProjectID: "proj-1", Priority: 2},
	}

	graph, err := MapToTaskGraph(project, issues)
	if err != nil {
		t.Fatalf("MapToTaskGraph returned error: %v", err)
	}

	children := graph.ChildrenOf("proj-1")
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	got := []string{children[0].ID, children[1].ID, children[2].ID}
	want := []string{"iss-a", "iss-c", "iss-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected child order %v, got %v", want, got)
	}
}

func TestMapToTaskGraphRejectsDuplicateIssueIDs(t *testing.T) {
	project := Project{ID: "proj-1", Name: "Roadmap"}
	issues := []Issue{
		{ID: "iss-1", ProjectID: "proj-1"},
		{ID: "iss-1", ProjectID: "proj-1"},
	}

	if _, err := MapToTaskGraph(project, issues); err == nil {
		t.Fatalf("expected duplicate IDs to fail")
	}
}
