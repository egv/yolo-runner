package docs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"gopkg.in/yaml.v3"
)

type agentBackendCapabilitiesFixture struct {
	Languages []string `json:"languages" yaml:"languages"`
	Features  []string `json:"features" yaml:"features"`
}

type agentBackendFixture struct {
	Name         string                          `json:"name" yaml:"name"`
	Type         string                          `json:"type" yaml:"type"`
	Backend      string                          `json:"backend" yaml:"backend"`
	Model        string                          `json:"model" yaml:"model"`
	Adapter      string                          `json:"adapter" yaml:"adapter"`
	Capabilities agentBackendCapabilitiesFixture `json:"capabilities" yaml:"capabilities"`
	Config       map[string]any                  `json:"config" yaml:"config"`
	Health       map[string]any                  `json:"health" yaml:"health"`
}

func TestAgentBackendSchemaDefinesUnifiedFields(t *testing.T) {
	schemaText := readRepoFile(t, "docs", "agent-backend-schema.json")
	var schema map[string]any
	if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
		t.Fatalf("parse agent backend schema JSON: %v", err)
	}
	_, err := compileAgentBackendSchema(schemaText)
	if err != nil {
		t.Fatalf("compile agent backend schema: %v", err)
	}

	defs, ok := asMap(schema["$defs"])
	if !ok {
		t.Fatal("agent backend schema missing $defs section")
	}
	agentBackend, ok := asMap(defs["agent_backend"])
	if !ok {
		t.Fatal("agent backend schema missing agent_backend definition")
	}
	required := asStringSlice(agentBackend["required"])
	for _, field := range []string{"name", "type", "backend", "model", "capabilities", "config"} {
		if !containsString(required, field) {
			t.Fatalf("agent backend schema missing required field %q", field)
		}
	}
	if !strings.Contains(schemaText, `"api_key_env"`) {
		t.Fatal("agent backend schema should document api_key_env backend setting")
	}
	if !strings.Contains(schemaText, `"languages"`) {
		t.Fatal("agent backend schema should document capabilities.languages")
	}
	if !strings.Contains(schemaText, `"features"`) {
		t.Fatal("agent backend schema should document capabilities.features")
	}
	if !strings.Contains(schemaText, `"health"`) {
		t.Fatal("agent backend schema should document health check settings")
	}
}

func TestAgentBackendValidationExamplesAreComplete(t *testing.T) {
	schemaText := readRepoFile(t, "docs", "agent-backend-schema.json")
	schema, err := compileAgentBackendSchema(schemaText)
	if err != nil {
		t.Fatalf("compile agent backend schema: %v", err)
	}

	raw := readRepoFile(t, "docs", "agent-backends-valid.json")
	var fixtureDocuments []any
	if err := json.Unmarshal([]byte(raw), &fixtureDocuments); err != nil {
		t.Fatalf("parse valid agent backend example JSON: %v", err)
	}
	if err := schema.Validate(fixtureDocuments); err != nil {
		t.Fatalf("valid example should satisfy schema: %v", summarizeSchemaError(err))
	}

	var fixtures []agentBackendFixture
	if err := json.Unmarshal([]byte(raw), &fixtures); err != nil {
		t.Fatalf("parse valid agent backend example JSON: %v", err)
	}
	if len(fixtures) != 5 {
		t.Fatalf("expected 5 example backends, got %d", len(fixtures))
	}

	expected := map[string]struct{}{
		"codex":    {},
		"opencode": {},
		"claude":   {},
		"kimi":     {},
		"gemini":   {},
	}
	for _, fixture := range fixtures {
		issues := validateAgentBackendFixture(t, fixture)
		if len(issues) > 0 {
			t.Fatalf("expected valid example for backend %q, got errors: %s", fixture.Name, strings.Join(issues, "; "))
		}
		if _, ok := expected[fixture.Name]; !ok {
			t.Fatalf("unexpected backend example %q in valid fixture list", fixture.Name)
		}
		delete(expected, fixture.Name)
	}
	for backend := range expected {
		t.Fatalf("missing valid example for required backend %q", backend)
	}
}

func TestAgentBackendInvalidFixturesAreRejected(t *testing.T) {
	schemaText := readRepoFile(t, "docs", "agent-backend-schema.json")
	schema, err := compileAgentBackendSchema(schemaText)
	if err != nil {
		t.Fatalf("compile agent backend schema: %v", err)
	}

	raw := readRepoFile(t, "docs", "agent-backends-invalid.yaml")
	var fixtureDocuments []any
	if err := yaml.Unmarshal([]byte(raw), &fixtureDocuments); err != nil {
		t.Fatalf("parse invalid YAML fixture: %v", err)
	}
	if len(fixtureDocuments) != 1 {
		t.Fatalf("expected one fixture in invalid YAML file, got %d", len(fixtureDocuments))
	}
	if err := schema.Validate(fixtureDocuments); err == nil {
		t.Fatalf("expected invalid fixture to fail validation")
	} else if !strings.Contains(strings.ToLower(summarizeSchemaError(err)), "languages") {
		t.Fatalf("expected invalid fixture to fail on capabilities.languages, got %s", strings.TrimSpace(summarizeSchemaError(err)))
	}

	var fixtures []agentBackendFixture
	if err := yaml.Unmarshal([]byte(raw), &fixtures); err != nil {
		t.Fatalf("parse invalid YAML fixture: %v", err)
	}
	if len(fixtures) != 1 {
		t.Fatalf("expected one fixture in invalid YAML file, got %d", len(fixtures))
	}
	issues := validateAgentBackendFixture(t, fixtures[0])
	if len(issues) == 0 {
		t.Fatalf("expected invalid fixture to fail validation")
	}
	if !containsString(issues, "languages") {
		t.Fatalf("expected invalid fixture to fail on capabilities.languages, got %s", strings.Join(issues, "; "))
	}
}

func compileAgentBackendSchema(schemaText string) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("agent-backend-schema.json", strings.NewReader(schemaText)); err != nil {
		return nil, fmt.Errorf("load schema resource: %w", err)
	}
	compiled, err := compiler.Compile("agent-backend-schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return compiled, nil
}

func summarizeSchemaError(err error) string {
	if err == nil {
		return ""
	}
	var lines []string
	lines = append(lines, err.Error())
	return strings.Join(lines, "; ")
}

func validateAgentBackendFixture(t *testing.T, fixture agentBackendFixture) []string {
	t.Helper()

	issues := []string{}
	if strings.TrimSpace(fixture.Name) == "" {
		issues = append(issues, "name is required")
	}
	if strings.TrimSpace(fixture.Type) == "" {
		issues = append(issues, "type is required")
	}
	if strings.TrimSpace(fixture.Backend) == "" {
		issues = append(issues, "backend is required")
	}
	if strings.TrimSpace(fixture.Model) == "" {
		issues = append(issues, "model is required")
	}
	langs := trimStrings(fixture.Capabilities.Languages)
	if len(langs) == 0 {
		issues = append(issues, "capabilities.languages must be non-empty")
	}
	features := trimStrings(fixture.Capabilities.Features)
	if len(features) == 0 {
		issues = append(issues, "capabilities.features must be non-empty")
	}
	for _, feature := range features {
		switch feature {
		case "implement", "review", "stream", "larger_model", "rewrite_task", "service_proxy":
		default:
			issues = append(issues, "capabilities.features contains unsupported feature: "+feature)
		}
	}
	if len(fixture.Config) == 0 {
		issues = append(issues, "config is required")
	} else {
		if rawTimeout, ok := fixture.Config["timeout"]; ok {
			value := strings.TrimSpace(configValueString(rawTimeout))
			if value == "" {
				issues = append(issues, "config.timeout cannot be empty")
			}
		}
		if rawAPI, ok := fixture.Config["api_key_env"]; ok {
			value := strings.TrimSpace(configValueString(rawAPI))
			if value == "" {
				issues = append(issues, "config.api_key_env cannot be empty")
			}
		}
	}
	if fixture.Health != nil && strings.TrimSpace(stringFromAny(fixture.Health["enabled"])) == "" {
		issues = append(issues, "health.enabled must be set")
	}
	return issues
}

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func configValueString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case string:
		return strings.TrimSpace(typed)
	}
	return ""
}

func TestCodexExampleReflectsServerBackedDefaults(t *testing.T) {
	raw := readRepoFile(t, "docs", "agent-backends-valid.json")
	var fixtures []agentBackendFixture
	if err := json.Unmarshal([]byte(raw), &fixtures); err != nil {
		t.Fatalf("parse agent-backends-valid.json: %v", err)
	}

	var codex *agentBackendFixture
	for i := range fixtures {
		if fixtures[i].Name == "codex" {
			codex = &fixtures[i]
			break
		}
	}
	if codex == nil {
		t.Fatal("codex backend not found in agent-backends-valid.json")
	}

	if codex.Adapter != "codex-app-server" {
		t.Errorf("codex example adapter = %q, want %q", codex.Adapter, "codex-app-server")
	}
	if codex.Health == nil {
		t.Error("codex example missing health block")
	} else if stringFromAny(codex.Health["enabled"]) != "true" {
		t.Errorf("codex example health.enabled = %v, want true", codex.Health["enabled"])
	}
}
