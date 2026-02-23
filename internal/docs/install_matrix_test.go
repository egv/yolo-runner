package docs

import (
	"fmt"
	"strings"
	"testing"
)

func splitMarkdownRow(row string) []string {
	row = strings.TrimSpace(row)
	if strings.HasPrefix(row, "|") {
		row = strings.TrimPrefix(row, "|")
	}
	if strings.HasSuffix(row, "|") {
		row = strings.TrimSuffix(row, "|")
	}
	return strings.FieldsFunc(strings.TrimSpace(row), func(r rune) bool {
		return r == '|'
	})
}

func TestInstallMatrixDefinesSupportedPlatformsAndShellNotes(t *testing.T) {
	matrix := readRepoFile(t, "docs", "install-matrix.md")

	requiredPlatformEntries := map[string]bool{
		"macOS|amd64":  false,
		"macOS|arm64":  false,
		"Linux|amd64":  false,
		"Linux|arm64":  false,
		"Windows|amd64": false,
	}
	for _, line := range strings.Split(matrix, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "| ---") || strings.Contains(strings.ToLower(line), "platform") {
			continue
		}

		cells := splitMarkdownRow(line)
		if len(cells) < 3 {
			continue
		}

		platform := strings.TrimSpace(cells[0])
		arch := strings.TrimSpace(cells[1])
		key := platform + "|" + arch
		if _, ok := requiredPlatformEntries[key]; ok {
			requiredPlatformEntries[key] = true
		}

		if strings.TrimSpace(cells[2]) == "" {
			continue
		}
	}

	for platformArch, seen := range requiredPlatformEntries {
		if !seen {
			t.Fatalf("install matrix missing platform entry %s", platformArch)
		}
	}

	// shell notes should cover each platform family used in the matrix.
	requiredShellNotes := []string{"bash", "PowerShell", "pwsh", "sh"}
	for _, note := range requiredShellNotes {
		if strings.Contains(matrix, note) {
			return
		}
	}
	t.Fatal("install matrix missing shell notes (expected bash/sh and PowerShell references)")
}

func TestInstallMatrixEntriesIncludeCommandAndSuccessCriteria(t *testing.T) {
	matrix := readRepoFile(t, "docs", "install-matrix.md")

	requiredHeaders := []string{
		"make install command",
		"make install success",
		"release artifact command",
		"release artifact success",
		"install script command",
		"install script success",
	}
	for _, needle := range requiredHeaders {
		if !strings.Contains(strings.ToLower(matrix), strings.ToLower(needle)) {
			t.Fatalf("install matrix missing header/column %q", needle)
		}
	}

	hasContentRows := 0
	for _, line := range strings.Split(matrix, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "| ---") || strings.Contains(strings.ToLower(line), "platform") {
			continue
		}

		cells := splitMarkdownRow(line)
		if len(cells) < 9 {
			continue
		}

		commandColumnIndexes := []int{3, 5, 7}
		successColumnIndexes := []int{4, 6, 8}
		for _, i := range commandColumnIndexes {
			if strings.TrimSpace(cells[i]) == "" {
				t.Fatalf("matrix entry missing command column %d in row %q", i, line)
			}
		}
		for _, i := range successColumnIndexes {
			if strings.TrimSpace(cells[i]) == "" {
				t.Fatalf("matrix entry missing success criteria column %d in row %q", i, line)
			}
		}

		for _, note := range []int{3, 4, 5, 6, 7, 8} {
			if fmt.Sprintf("%q", strings.TrimSpace(cells[note])) == "\"N/A\"" {
				t.Fatalf("matrix entry row %q leaves required field as N/A", line)
			}
		}
		hasContentRows++
	}

	if hasContentRows < 5 {
		t.Fatalf("expected at least 5 data rows in install matrix, got %d", hasContentRows)
	}
}

func TestInstallMatrixUsesCurrentRepositoryForReleaseAndInstallerURLs(t *testing.T) {
	matrix := readRepoFile(t, "docs", "install-matrix.md")

	requiredStrings := []string{
		"https://github.com/egv/yolo-runner/releases/latest/download",
		"https://raw.githubusercontent.com/egv/yolo-runner/main/install.sh",
	}
	for _, needle := range requiredStrings {
		if !strings.Contains(matrix, needle) {
			t.Fatalf("install matrix missing expected URL %q", needle)
		}
	}
}
