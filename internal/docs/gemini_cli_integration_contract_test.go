package docs

import (
	"strings"
	"testing"
)

func TestGeminiCLIIntegrationContractDocumentsInvocationACPAndEventMapping(t *testing.T) {
	contract := readRepoFile(t, "docs", "gemini-spike-integration-contract.md")
	requiredSections := []string{
		"# Gemini CLI Integration Contract (Task #161)",
		"## Non-interactive execution",
		"## Streaming progress/events",
		"--output-format stream-json",
		"--output-format json",
		"## Final result extraction",
		"## ACP status",
		"--experimental-acp",
		"## Env vars",
		"GEMINI_API_KEY",
		"GOOGLE_API_KEY",
		"GOOGLE_GENAI_USE_VERTEXAI",
		"## Event mapping",
		"runner_output",
		"runner_progress",
		"runner_warning",
		"runner_finished",
		"thought",
		"metadata[\"phase\"]=tool_call",
		"## Smoke task",
		"Deterministic ACP smoke check",
		"timeout 10s",
		"protocolVersion",
	}

	for _, needle := range requiredSections {
		if !strings.Contains(contract, needle) {
			t.Fatalf("gemini integration contract missing expected section or signal: %q", needle)
		}
	}
}
