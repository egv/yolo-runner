package docs

import (
	"strings"
	"testing"
)

func TestRoadmapDocumentsLinearDeferredV21Scope(t *testing.T) {
	roadmap := readRepoFile(t, "V2_IMPROVEMENTS.md")

	required := []string{
		"Linear agent deferred scope (v2.1)",
		"issueRepositorySuggestions",
		"multi-workspace",
		"advanced activity types",
		"single-workspace MVP",
	}
	for _, needle := range required {
		if !strings.Contains(roadmap, needle) {
			t.Fatalf("V2 roadmap missing %q", needle)
		}
	}
}

func TestMigrationDocumentsLinearDeferredV21UpgradePath(t *testing.T) {
	migration := readRepoFile(t, "MIGRATION.md")

	required := []string{
		"Linear Agent Deferred Scope (v2.1)",
		"issueRepositorySuggestions",
		"multi-workspace",
		"advanced activity types",
		"Migration path to v2.1",
	}
	for _, needle := range required {
		if !strings.Contains(migration, needle) {
			t.Fatalf("MIGRATION missing %q", needle)
		}
	}
}
