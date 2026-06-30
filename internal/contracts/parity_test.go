package contracts_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/claude"
	"github.com/egv/yolo-runner/v2/internal/codex"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/kimi"
	"github.com/egv/yolo-runner/v2/internal/opencode"
)

func TestBackendParityFixturesEmitSameCanonicalEvents(t *testing.T) {
	want := []canonicalProgress{
		{Type: string(contracts.EventTypeAgentText), Identity: "explore"},
		{Type: string(contracts.EventTypeToolInvoked), Identity: "edit:internal/parity/example.go"},
		{Type: string(contracts.EventTypeAgentBlocked), Identity: "permission_denied:approval-denied-1"},
		{Type: string(contracts.EventTypeAgentFinished), Identity: "finished"},
	}

	tests := []struct {
		name    string
		fixture string
		replay  func(string) ([]contracts.RunnerProgress, error)
	}{
		{
			name:    "claude stream-json",
			fixture: "testdata/claude-parity.jsonl",
			replay:  replayClaudeNativeJSONL,
		},
		{
			name:    "opencode ACP",
			fixture: "testdata/opencode-parity.jsonl",
			replay:  replayOpenCodeNativeJSONL,
		},
		{
			name:    "codex app-server",
			fixture: "testdata/codex-parity.jsonl",
			replay:  replayCodexNativeJSONL,
		},
		{
			name:    "kimi stream-json",
			fixture: "testdata/kimi-parity.jsonl",
			replay:  replayKimiNativeJSONL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			progress, err := tt.replay(tt.fixture)
			if err != nil {
				t.Fatalf("replay fixture: %v", err)
			}

			got := canonicalizeParityProgress(progress)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("canonical progress mismatch\nwant: %#v\ngot:  %#v\nall progress: %#v", want, got, progress)
			}

			blocked := got[2]
			if blocked.Type != string(contracts.EventTypeAgentBlocked) || blocked.Identity != "permission_denied:approval-denied-1" {
				t.Fatalf("expected agent_blocked permission_denied for every backend, got %#v", blocked)
			}
		})
	}
}

type canonicalProgress struct {
	Type     string
	Identity string
}

func replayClaudeNativeJSONL(path string) ([]contracts.RunnerProgress, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return claude.NormalizeNativeStreamJSONL(file)
}

func replayOpenCodeNativeJSONL(path string) ([]contracts.RunnerProgress, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return opencode.NormalizeNativeACPJSONL(file)
}

func replayCodexNativeJSONL(path string) ([]contracts.RunnerProgress, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return codex.NormalizeNativeAppServerJSONL(file, contracts.RunnerModeImplement)
}

func replayKimiNativeJSONL(path string) ([]contracts.RunnerProgress, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return kimi.NormalizeNativeStreamJSONL(file)
}

func canonicalizeParityProgress(progress []contracts.RunnerProgress) []canonicalProgress {
	out := []canonicalProgress{}
	for _, p := range progress {
		switch p.Type {
		case string(contracts.EventTypeAgentText):
			if p.Metadata["parity_step"] == "explore" {
				out = append(out, canonicalProgress{Type: p.Type, Identity: "explore"})
			}
		case string(contracts.EventTypeToolInvoked):
			if p.Metadata["target"] == "internal/parity/example.go" || p.Metadata["path"] == "internal/parity/example.go" {
				out = append(out, canonicalProgress{Type: p.Type, Identity: "edit:internal/parity/example.go"})
			}
		case string(contracts.EventTypeAgentBlocked):
			if p.Metadata["reason"] == string(contracts.BlockReasonPermissionDenied) {
				out = append(out, canonicalProgress{Type: p.Type, Identity: "permission_denied:" + p.Metadata["approval_id"]})
			}
		case string(contracts.EventTypeAgentFinished):
			out = append(out, canonicalProgress{Type: p.Type, Identity: "finished"})
		}
	}
	return out
}
