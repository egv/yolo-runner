package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasePlaybookExistsForV2_4_1(t *testing.T) {
	_, err := os.ReadFile(filepath.Join("..", "..", "docs", "release-playbook.md"))
	if err != nil {
		t.Fatalf("expected docs/release-playbook.md to exist: %v", err)
	}
}

func TestReleasePlaybookCoversPreflightAndTagging(t *testing.T) {
	playbook := readRepoFile(t, "docs", "release-playbook.md")

	required := []string{
		"## Preflight",
		"git status --short",
		"go test ./...",
		"go build ./...",
		"make release-gate-e8",
		"## Tagging",
		"git tag -a v2.4.1 -m \"Release v2.4.1\"",
		"git push origin v2.4.1",
	}
	for _, needle := range required {
		if !strings.Contains(playbook, needle) {
			t.Fatalf("release playbook missing %q", needle)
		}
	}
}

func TestReleasePlaybookCoversVerificationAndSmokeInstall(t *testing.T) {
	playbook := readRepoFile(t, "docs", "release-playbook.md")

	required := []string{
		"## Verify Release Assets and Checksums",
		"gh release view",
		"checksums-",
		"sha256sum",
		"## Smoke Install and Update Check",
		"yolo-runner update --check",
	}
	for _, needle := range required {
		if !strings.Contains(playbook, needle) {
			t.Fatalf("release playbook missing %q", needle)
		}
	}
}

func TestMakefileDefinesReleaseV241Target(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	makefilePath := filepath.Join(repoRoot, "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	makefile := string(contents)
	if !strings.Contains(makefile, "release-v2.4.1:") {
		t.Fatalf("Makefile missing release-v2.4.1 target")
	}
	required := []string{
		"git status --short",
		"go test ./...",
		"go build ./...",
		"make release-gate-e8",
		"git tag -a v2.4.1",
		"git push origin v2.4.1",
	}
	for _, needle := range required {
		if !strings.Contains(makefile, needle) {
			t.Fatalf("Makefile release target missing %q", needle)
		}
	}
}
