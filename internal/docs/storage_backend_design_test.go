package docs

import (
	"strings"
	"testing"
)

func TestADR001DocumentsStorageBackendAndTaskEngineSeparation(t *testing.T) {
	adr := readRepoFile(t, "docs", "adr", "ADR-001-task-subsystem-refactoring.md")

	required := []string{
		"type StorageBackend interface {",
		"GetTaskTree(ctx context.Context, rootID string) (*TaskTree, error)",
		"GetTask(ctx context.Context, taskID string) (*Task, error)",
		"SetTaskStatus(ctx context.Context, taskID string, status TaskStatus) error",
		"SetTaskData(ctx context.Context, taskID string, data map[string]string) error",
		"StorageBackend handles persistence and retrieval of task data only.",
		"Storage and persistence remain in StorageBackend.",
	}

	for _, needle := range required {
		if !strings.Contains(adr, needle) {
			t.Fatalf("ADR-001 missing storage/task-engine separation detail: %q", needle)
		}
	}
}
