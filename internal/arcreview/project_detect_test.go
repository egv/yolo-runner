package arcreview

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectDetectFindsDominantNearestYaMakeRoot(t *testing.T) {
	workspace := t.TempDir()
	writeProjectDetectTestFile(t, workspace, "taxi/ya.make")
	writeProjectDetectTestFile(t, workspace, "taxi/backend-cpp/ya.make")
	writeProjectDetectTestFile(t, workspace, "taxi/backend-cpp/services/ai_minion/ya.make")
	writeProjectDetectTestFile(t, workspace, "taxi/backend-cpp/services/other_service/ya.make")

	got, err := DetectProjectContext(workspace, []PRChangedFile{
		{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		{Path: "taxi/backend-cpp/services/ai_minion/tests/main_ut.cpp", Status: "modified"},
		{Path: "taxi/backend-cpp/services/other_service/handler.cpp", Status: "modified"},
	})
	if err != nil {
		t.Fatalf("DetectProjectContext() error = %v", err)
	}

	want := ProjectContext{
		Root:    "taxi/backend-cpp/services/ai_minion",
		Command: []string{"ya", "make", "-t", "taxi/backend-cpp/services/ai_minion"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DetectProjectContext() = %#v, want %#v", got, want)
	}
}

func writeProjectDetectTestFile(t *testing.T, workspace string, rel string) {
	t.Helper()

	path := filepath.Join(workspace, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("# test fixture\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
