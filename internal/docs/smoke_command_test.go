package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileHasAgentTUISmokeTarget(t *testing.T) {
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
	if !strings.Contains(makefile, "smoke-agent-tui:") {
		t.Fatalf("Makefile missing smoke-agent-tui target")
	}
	if !strings.Contains(makefile, "go test ./cmd/yolo-agent ./cmd/yolo-tui") {
		t.Fatalf("smoke-agent-tui target must run yolo-agent/yolo-tui smoke tests")
	}
}
