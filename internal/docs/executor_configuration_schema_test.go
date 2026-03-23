package docs

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	executorConfigRequiredStages = []string{"quality_gate", "execute", "qc_gate", "complete"}
	executorConfigValidActions   = map[string]struct{}{
		"next":     {},
		"retry":    {},
		"fail":     {},
		"complete": {},
	}
	executorConfigValidNextStages = map[string]struct{}{
		"quality_gate": {},
		"execute":      {},
		"qc_gate":      {},
		"complete":     {},
	}
	executorConfigConditionPattern = regexp.MustCompile(`^\s*(?:true|false|tests_failed|review_failed|quality_score\s*(?:==|!=|>=|<=|>|<)\s*(?:[0-9]+(?:\.[0-9]+)?|threshold))\s*$`)
)

type executorConfigFixture struct {
	Name     string                   `json:"name" yaml:"name"`
	Type     string                   `json:"type" yaml:"type"`
	Backend  string                   `json:"backend" yaml:"backend"`
	Pipeline map[string]executorStage `json:"pipeline" yaml:"pipeline"`
}

type executorStage struct {
	Tools       []string                 `json:"tools" yaml:"tools"`
	Retry       executorRetryPolicy      `json:"retry" yaml:"retry"`
	Transitions executorStageTransitions `json:"transitions" yaml:"transitions"`
}

type executorRetryPolicy struct {
	MaxAttempts    int `json:"max_attempts" yaml:"max_attempts"`
	InitialDelayMs int `json:"initial_delay_ms" yaml:"initial_delay_ms"`
}

type executorStageTransitions struct {
	OnSuccess executorTransition `json:"on_success" yaml:"on_success"`
	OnFailure executorTransition `json:"on_failure" yaml:"on_failure"`
}

type executorTransition struct {
	Action    string `json:"action" yaml:"action"`
	NextStage string `json:"next_stage,omitempty" yaml:"next_stage,omitempty"`
	Condition string `json:"condition" yaml:"condition"`
}

func TestExecutorConfigurationSchemaDefinesRequiredStructure(t *testing.T) {
	schemaText := readRepoFile(t, "docs", "executor-configuration-schema.json")
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
		t.Fatalf("parse executor schema JSON: %v", err)
	}

	required := asStringSlice(schema["required"])
	for _, field := range []string{"name", "type", "backend", "pipeline"} {
		if !contains(required, field) {
			t.Fatalf("executor schema missing required top-level field %q", field)
		}
	}

	defs, ok := asMap(schema["$defs"])
	if !ok {
		t.Fatalf("executor schema missing $defs section")
	}
	pipelineStages, ok := asMap(defs["pipeline_stages"])
	if !ok {
		t.Fatalf("executor schema missing pipeline_stages definition")
	}
	props, ok := asMap(pipelineStages["properties"])
	if !ok {
		t.Fatalf("executor schema missing pipeline stage properties")
	}
	for _, stage := range executorConfigRequiredStages {
		if _, ok := props[stage]; !ok {
			t.Fatalf("executor schema missing stage %q", stage)
		}
	}
	if !strings.Contains(schemaText, `"quality_score < threshold"`) {
		t.Fatalf("executor schema examples should include quality_score < threshold")
	}
	if !strings.Contains(schemaText, `"tests_failed"`) {
		t.Fatalf("executor schema examples should include tests_failed")
	}
	if !strings.Contains(schemaText, `"review_failed"`) {
		t.Fatalf("executor schema examples should include review_failed")
	}
}

func TestExecutorConfigurationExamplesValidate(t *testing.T) {
	validJSON := loadExecutorConfigFixture(t, filepath.Join("docs", "executor-configuration-valid.json"))
	validErrors := validateExecutorConfig(validJSON)
	if len(validErrors) > 0 {
		t.Fatalf("expected valid JSON example to validate, got: %s", strings.Join(validErrors, "; "))
	}

	invalidYAML := loadExecutorConfigFixture(t, filepath.Join("docs", "executor-configuration-invalid.yaml"))
	invalidErrors := validateExecutorConfig(invalidYAML)
	if len(invalidErrors) == 0 {
		t.Fatalf("expected invalid YAML example to fail schema validation")
	}
	if !containsString(invalidErrors, "name must be non-empty") {
		t.Fatalf("expected invalid YAML to report empty name, got: %s", strings.Join(invalidErrors, "; "))
	}
	if !containsString(invalidErrors, "quality_gate.tools must include at least one tool") {
		t.Fatalf("expected invalid YAML to report missing tools, got: %s", strings.Join(invalidErrors, "; "))
	}
}

func TestExecutorConfigurationSchemaDocumentIncludesExamples(t *testing.T) {
	docsText := readRepoFile(t, "docs", "executor-configuration.md")
	required := []string{
		"quality_gate",
		"execute",
		"qc_gate",
		"complete",
		"on_success",
		"on_failure",
		"`quality_score < threshold`",
		"`tests_failed`",
		"`review_failed`",
	}
	for _, needle := range required {
		if !strings.Contains(docsText, needle) {
			t.Fatalf("executor configuration docs missing required content: %q", needle)
		}
	}
}

func loadExecutorConfigFixture(t *testing.T, relPath string) executorConfigFixture {
	t.Helper()
	raw := readRepoFile(t, relPath)
	var cfg executorConfigFixture
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal executor config fixture %s: %v", relPath, err)
	}
	return cfg
}

func validateExecutorConfig(cfg executorConfigFixture) []string {
	var issues []string
	if strings.TrimSpace(cfg.Name) == "" {
		issues = append(issues, "name must be non-empty")
	}
	if strings.TrimSpace(cfg.Type) == "" {
		issues = append(issues, "type must be non-empty")
	}
	if strings.TrimSpace(cfg.Backend) == "" {
		issues = append(issues, "backend must be non-empty")
	}
	if len(cfg.Pipeline) == 0 {
		issues = append(issues, "pipeline must be present")
		return issues
	}
	for _, stage := range executorConfigRequiredStages {
		if _, ok := cfg.Pipeline[stage]; !ok {
			issues = append(issues, fmt.Sprintf("pipeline missing stage %q", stage))
		}
	}
	if len(cfg.Pipeline) != len(executorConfigRequiredStages) {
		issues = append(issues, "pipeline must not include unknown stages")
	}
	for stageName, stage := range cfg.Pipeline {
		issues = append(issues, validateExecutorStage(stageName, stage)...)
	}
	return issues
}

func validateExecutorStage(stageName string, stage executorStage) []string {
	var issues []string
	if len(stage.Tools) == 0 {
		issues = append(issues, fmt.Sprintf("%s.tools must include at least one tool", stageName))
	}
	for _, tool := range stage.Tools {
		if strings.TrimSpace(tool) == "" {
			issues = append(issues, fmt.Sprintf("%s.tools entry must be non-empty", stageName))
		}
	}
	if stage.Retry.MaxAttempts < 1 {
		issues = append(issues, fmt.Sprintf("%s.retry.max_attempts must be at least 1", stageName))
	}
	if stage.Retry.InitialDelayMs < 0 {
		issues = append(issues, fmt.Sprintf("%s.retry.initial_delay_ms must be >= 0", stageName))
	}
	if stage.Transitions.OnSuccess.Action == "" || stage.Transitions.OnFailure.Action == "" {
		issues = append(issues, fmt.Sprintf("%s.transitions must include on_success and on_failure actions", stageName))
	}
	issues = append(issues, validateExecutorTransition(fmt.Sprintf("%s.on_success", stageName), stage.Transitions.OnSuccess)...)
	issues = append(issues, validateExecutorTransition(fmt.Sprintf("%s.on_failure", stageName), stage.Transitions.OnFailure)...)
	return issues
}

func validateExecutorTransition(field string, transition executorTransition) []string {
	var issues []string
	if transition.Action == "" {
		issues = append(issues, fmt.Sprintf("%s.action must be defined", field))
		return issues
	}
	if _, ok := executorConfigValidActions[transition.Action]; !ok {
		issues = append(issues, fmt.Sprintf("%s.action %q is invalid", field, transition.Action))
	}
	if !executorConfigConditionPattern.MatchString(transition.Condition) {
		issues = append(issues, fmt.Sprintf("%s.condition %q is not a supported deterministic expression", field, transition.Condition))
	}
	if transition.Action == "next" {
		if strings.TrimSpace(transition.NextStage) == "" {
			issues = append(issues, fmt.Sprintf("%s.next_stage is required for action next", field))
		} else if _, ok := executorConfigValidNextStages[transition.NextStage]; !ok {
			issues = append(issues, fmt.Sprintf("%s.next_stage %q is invalid", field, transition.NextStage))
		}
	}
	return issues
}

func asMap(v any) (map[string]any, bool) {
	casted, ok := v.(map[string]any)
	return casted, ok
}

func asStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, val := range raw {
		text, ok := val.(string)
		if !ok {
			continue
		}
		out = append(out, text)
	}
	return out
}

func contains(items []string, item string) bool {
	for _, candidate := range items {
		if candidate == item {
			return true
		}
	}
	return false
}

func containsString(items []string, needle string) bool {
	for _, value := range items {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
