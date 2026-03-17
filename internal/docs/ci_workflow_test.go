package docs

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type ciWorkflow struct {
	On   map[string]interface{} `yaml:"on"`
	Jobs map[string]ciJob       `yaml:"jobs"`
}

type ciJob struct {
	Steps []ciStep `yaml:"steps"`
}

type ciStep struct {
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

func TestCIWorkflowRunsTestsOnPullRequestAndMainPush(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")

	var parsed ciWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("unmarshal workflow YAML: %v", err)
	}

	if _, ok := parsed.On["pull_request"]; !ok {
		t.Fatalf("ci workflow must run on pull_request")
	}

	pushNode, ok := parsed.On["push"]
	if !ok {
		t.Fatalf("ci workflow must run on push")
	}
	pushConfig, ok := pushNode.(map[string]interface{})
	if !ok {
		t.Fatalf("ci workflow push event config should be a mapping")
	}
	branches, ok := pushConfig["branches"].([]interface{})
	if !ok {
		t.Fatalf("ci workflow push trigger should list branches")
	}
	hasMain := false
	for _, branch := range branches {
		if branch == "main" {
			hasMain = true
		}
	}
	if !hasMain {
		t.Fatalf("ci workflow push should include main branch")
	}

	foundSetupGo := false
	hasGoTest := false
	for _, job := range parsed.Jobs {
		steps := job.Steps
		hasSetupGo := false
		for _, step := range steps {
			if strings.Contains(step.Uses, "actions/setup-go@") && step.With["go-version-file"] == "go.mod" {
				hasSetupGo = true
				break
			}
		}
		if !hasSetupGo {
			continue
		}
		foundSetupGo = true
		for _, step := range steps {
			if strings.Contains(step.Run, "go test ./...") {
				hasGoTest = true
				break
			}
		}
	}
	if !foundSetupGo {
		t.Fatalf("ci workflow missing actions/setup-go step with go-version-file: go.mod")
	}
	if !hasGoTest {
		t.Fatalf("ci workflow missing run step: go test ./...")
	}
}
