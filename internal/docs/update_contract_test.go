package docs

import (
	"strings"
	"testing"
)

func TestReadmeDocumentsUpdateUsageAndConstraints(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	lower := strings.ToLower(readme)

	required := []string{
		"## update",
		"--release",
		"--check",
		"pin to a specific tag",
		"/releases/latest",
		"checksums",
		"transactional",
		"not writable",
		"unsupported windows install path",
		"path",
		"--release-api",
	}

	for _, needle := range required {
		if !strings.Contains(lower, strings.ToLower(needle)) {
			t.Fatalf("README missing update contract phrase %q", needle)
		}
	}
}
