package runner

import "testing"

func intPtr(value int) *int {
	return &value
}

func TestSelectFirstOpenLeafTaskIDNestedEpic(t *testing.T) {
	root := Issue{
		ID:        "epic-root",
		IssueType: "epic",
		Status:    "open",
		Children: []Issue{
			{
				ID:        "epic-1",
				IssueType: "epic",
				Status:    "open",
				Priority:  intPtr(1),
				Children: []Issue{
					{
						ID:        "task-closed",
						IssueType: "task",
						Status:    "closed",
						Priority:  intPtr(1),
					},
					{
						ID:        "epic-1-1",
						IssueType: "epic",
						Status:    "open",
						Priority:  intPtr(2),
						Children: []Issue{
							{
								ID:        "task-nested",
								IssueType: "task",
								Status:    "open",
								Priority:  intPtr(1),
							},
						},
					},
				},
			},
			{
				ID:        "task-root",
				IssueType: "task",
				Status:    "open",
				Priority:  intPtr(2),
			},
		},
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID != "task-nested" {
		t.Fatalf("expected nested task-nested, got %q", leafID)
	}
}

func TestSelectFirstOpenLeafTaskIDPriorityOrdering(t *testing.T) {
	root := Issue{
		ID:        "epic-root",
		IssueType: "epic",
		Status:    "open",
		Children: []Issue{
			{
				ID:        "task-closed",
				IssueType: "task",
				Status:    "blocked",
				Priority:  intPtr(0),
			},
			{
				ID:        "task-high",
				IssueType: "task",
				Status:    "open",
				Priority:  intPtr(5),
			},
			{
				ID:        "task-low",
				IssueType: "task",
				Status:    "open",
				Priority:  intPtr(1),
			},
			{
				ID:        "task-missing",
				IssueType: "task",
				Status:    "open",
			},
		},
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID != "task-low" {
		t.Fatalf("expected task-low, got %q", leafID)
	}
}

func TestSelectFirstOpenLeafTaskIDSkipsEmptyEpic(t *testing.T) {
	root := Issue{
		ID:        "epic-root",
		IssueType: "epic",
		Status:    "open",
		Children: []Issue{
			{
				ID:        "epic-empty",
				IssueType: "epic",
				Status:    "open",
				Priority:  intPtr(0),
			},
			{
				ID:        "task-open",
				IssueType: "task",
				Status:    "open",
				Priority:  intPtr(1),
			},
		},
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID != "task-open" {
		t.Fatalf("expected task-open, got %q", leafID)
	}
}

func TestSelectFirstOpenLeafTaskIDTaskRoot(t *testing.T) {
	root := Issue{
		ID:        "task-root",
		IssueType: "task",
		Status:    "open",
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID != "task-root" {
		t.Fatalf("expected task-root, got %q", leafID)
	}
}

func TestSelectFirstOpenLeafTaskIDAcceptsBugLeaf(t *testing.T) {
	root := Issue{
		ID:        "epic-root",
		IssueType: "epic",
		Status:    "open",
		Children: []Issue{
			{
				ID:        "bug-1",
				IssueType: "bug",
				Status:    "open",
			},
		},
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID != "bug-1" {
		t.Fatalf("expected bug-1, got %q", leafID)
	}
}

func TestSelectFirstOpenLeafTaskIDSkipsEpicAndMoleculeAsLeaves(t *testing.T) {
	root := Issue{
		ID:        "epic-root",
		IssueType: "epic",
		Status:    "open",
		Children: []Issue{
			{
				ID:        "epic-1",
				IssueType: "epic",
				Status:    "open",
			},
			{
				ID:        "mol-1",
				IssueType: "molecule",
				Status:    "open",
			},
			{
				ID:        "task-1",
				IssueType: "task",
				Status:    "open",
			},
		},
	}

	leafID := SelectFirstOpenLeafTaskID(root)
	if leafID != "task-1" {
		t.Fatalf("expected task-1, got %q", leafID)
	}
}
