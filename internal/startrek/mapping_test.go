package startrek

import (
	"strings"
	"testing"
	"time"
)

func TestMapIssueToTaskPacksIssueContextIntoDescription(t *testing.T) {
	issue := Issue{
		ID:          "VAY-42",
		Title:       "Map tracker context",
		Description: "Issue body with implementation details.",
		Labels:      []string{"ready-for-yolo", "backend"},
		Author:      IssueAuthor{ID: "112233", Display: "Ada Lovelace"},
	}
	comments := []IssueComment{
		{
			ID:        "comment-1",
			Body:      "First implementation note.",
			Author:    IssueAuthor{ID: "445566", Display: "Grace Hopper"},
			CreatedAt: time.Date(2026, 5, 28, 1, 2, 3, 0, time.UTC),
		},
		{
			ID:        "comment-2",
			Body:      "Second implementation note.",
			Author:    IssueAuthor{ID: "112233", Display: "Ada Lovelace"},
			CreatedAt: time.Date(2026, 5, 28, 2, 3, 4, 0, time.UTC),
		},
	}

	task := MapIssueToTask(issue, comments, TaskMappingOptions{
		QueueKey: "VAY",
		RootID:   "VAY-1",
	})

	if task.ID != "VAY-42" {
		t.Fatalf("expected task ID VAY-42, got %q", task.ID)
	}
	if task.Title != "Map tracker context" {
		t.Fatalf("expected task title from issue title, got %q", task.Title)
	}
	if task.ParentID != "VAY-1" {
		t.Fatalf("expected parent ID from root, got %q", task.ParentID)
	}

	for _, want := range []string{
		"Title: Map tracker context",
		"Issue: VAY-42",
		"Queue: VAY",
		"Root: VAY-1",
		"Author: Ada Lovelace (112233)",
		"Labels: ready-for-yolo, backend",
		"Description:\nIssue body with implementation details.",
		"Recent comments:",
		"2026-05-28T01:02:03Z - Grace Hopper (445566): First implementation note.",
		"2026-05-28T02:03:04Z - Ada Lovelace (112233): Second implementation note.",
	} {
		if !strings.Contains(task.Description, want) {
			t.Fatalf("expected task description to contain %q, got:\n%s", want, task.Description)
		}
	}
}
