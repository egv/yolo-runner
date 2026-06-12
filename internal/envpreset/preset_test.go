package envpreset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDesignExampleAndRejectsUnknownEnums(t *testing.T) {
	t.Run("loads design-doc example", func(t *testing.T) {
		path := writePresetFile(t, `
presets:
  adapta:
    workspace: { strategy: arc-shared, mount: ~/arcadia, subpath: marvel/gena/adapta }
    landing:   { type: arc-pr, title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}" }
    agent:     { backend: codex, model: gpt-5.5, runner_timeout: 20m }
    limits:    { max_concurrent: 1 }
    env:       { passthrough: [STARTREK_TOKEN] }
  yolo-runner:
    workspace: { strategy: git-clone, origin: ~/yolo-runner, base_branch: main }
    landing:   { type: git-merge }
    agent:     { backend: codex, model: gpt-5.5 }
`)

		presets, err := Load(path)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}

		adapta := presets["adapta"]
		if adapta.Workspace.Strategy != WorkspaceStrategyArcShared {
			t.Fatalf("expected adapta strategy %q, got %q", WorkspaceStrategyArcShared, adapta.Workspace.Strategy)
		}
		if adapta.Workspace.Mount != "~/arcadia" || adapta.Workspace.Subpath != "marvel/gena/adapta" {
			t.Fatalf("unexpected adapta workspace: %#v", adapta.Workspace)
		}
		if adapta.Landing.Type != LandingTypeArcPR {
			t.Fatalf("expected adapta landing %q, got %q", LandingTypeArcPR, adapta.Landing.Type)
		}
		if adapta.Landing.TitleTemplate != "Land {{ .TaskID }}: {{ .TaskTitle }}" {
			t.Fatalf("unexpected title template: %q", adapta.Landing.TitleTemplate)
		}
		if adapta.Agent.Backend != "codex" || adapta.Agent.Model != "gpt-5.5" {
			t.Fatalf("unexpected adapta agent: %#v", adapta.Agent)
		}
		if adapta.Agent.RunnerTimeout != 20*time.Minute {
			t.Fatalf("expected runner timeout 20m, got %s", adapta.Agent.RunnerTimeout)
		}
		if adapta.Limits.MaxConcurrent != 1 {
			t.Fatalf("expected max_concurrent=1, got %d", adapta.Limits.MaxConcurrent)
		}
		if len(adapta.Env.Passthrough) != 1 || adapta.Env.Passthrough[0] != "STARTREK_TOKEN" {
			t.Fatalf("unexpected passthrough env: %#v", adapta.Env.Passthrough)
		}

		yolo := presets["yolo-runner"]
		if yolo.Workspace.Strategy != WorkspaceStrategyGitClone {
			t.Fatalf("expected yolo-runner strategy %q, got %q", WorkspaceStrategyGitClone, yolo.Workspace.Strategy)
		}
		if yolo.Workspace.Origin != "~/yolo-runner" || yolo.Workspace.BaseBranch != "main" {
			t.Fatalf("unexpected yolo-runner workspace: %#v", yolo.Workspace)
		}
		if yolo.Landing.Type != LandingTypeGitMerge {
			t.Fatalf("expected yolo-runner landing %q, got %q", LandingTypeGitMerge, yolo.Landing.Type)
		}
	})

	t.Run("rejects unknown workspace strategy clearly", func(t *testing.T) {
		path := writePresetFile(t, `
presets:
  bad:
    workspace: { strategy: svn-checkout }
    landing:   { type: none }
    agent:     { backend: codex }
`)

		_, err := Load(path)
		if err == nil {
			t.Fatal("expected unknown workspace strategy to fail")
		}
		assertErrorContains(t, err, `presets.bad.workspace.strategy`)
		assertErrorContains(t, err, `git-clone`)
		assertErrorContains(t, err, `arc-shared`)
		assertErrorContains(t, err, `path`)
	})

	t.Run("rejects unknown landing type clearly", func(t *testing.T) {
		path := writePresetFile(t, `
presets:
  bad:
    workspace: { strategy: path, path: /repo }
    landing:   { type: direct-push }
    agent:     { backend: codex }
`)

		_, err := Load(path)
		if err == nil {
			t.Fatal("expected unknown landing type to fail")
		}
		assertErrorContains(t, err, `presets.bad.landing.type`)
		assertErrorContains(t, err, `git-merge`)
		assertErrorContains(t, err, `arc-pr`)
		assertErrorContains(t, err, `none`)
	})
}

func writePresetFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "environments.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write preset file: %v", err)
	}
	return path
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()

	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error %q to contain %q", err.Error(), want)
	}
}
