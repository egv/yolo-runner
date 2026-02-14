package docs

import (
	"strings"
	"testing"
)

func TestRoadmapDocumentsYandexDeferredRationaleAndExtensionPoints(t *testing.T) {
	roadmap := readRepoFile(t, "V2_IMPROVEMENTS.md")

	required := []string{
		"deferred in E9",
		"no runtime integration in this wave",
		"Yandex defer rationale (E9)",
		"Yandex contract extension points",
		"cmd/yolo-agent/tracker_profile.go",
		"buildTaskManagerForTracker",
		"contracts.TaskManager",
		"internal/contracts/conformance/task_manager_suite.go",
	}
	for _, needle := range required {
		if !strings.Contains(roadmap, needle) {
			t.Fatalf("V2 roadmap missing %q", needle)
		}
	}
}
