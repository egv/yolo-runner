package workitem

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestImplementPayloadRoundTripAndDecode(t *testing.T) {
	payload := ImplementPayload{
		TaskID:      "ADAPTABOT-12",
		Title:       "Add queue split schemas",
		Description: "Implement payload/result contracts.",
		PromptContext: ImplementPromptContext{
			Prompt:   "Use strict TDD.",
			ParentID: "ADAPTABOT-1",
			Metadata: map[string]string{
				"queue": "adapta",
			},
		},
		BaseBranch: "main",
		RetryContext: ImplementRetryContext{
			Attempt:           2,
			PreviousReason:    "review rejected: missing tests",
			ReviewFeedback:    "add round-trip assertions",
			PreviousBranch:    "task/ADAPTABOT-12",
			PreviousCommitSHA: "abc123",
		},
		TDD:         true,
		QualityGate: true,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"task_id": "ADAPTABOT-12",
		"title": "Add queue split schemas",
		"description": "Implement payload/result contracts.",
		"prompt_context": {
			"prompt": "Use strict TDD.",
			"parent_id": "ADAPTABOT-1",
			"metadata": {"queue": "adapta"}
		},
		"base_branch": "main",
		"retry_context": {
			"attempt": 2,
			"previous_reason": "review rejected: missing tests",
			"review_feedback": "add round-trip assertions",
			"previous_branch": "task/ADAPTABOT-12",
			"previous_commit_sha": "abc123"
		},
		"tdd": true,
		"quality_gate": true
	}`))

	got, err := DecodeImplementPayload(append(raw, []byte(`{"future_field":"ignored"}`)...))
	if err == nil {
		t.Fatalf("expected trailing JSON error")
	}

	withUnknown := []byte(`{
		"task_id": "ADAPTABOT-12",
		"title": "Add queue split schemas",
		"description": "Implement payload/result contracts.",
		"prompt_context": {
			"prompt": "Use strict TDD.",
			"parent_id": "ADAPTABOT-1",
			"metadata": {"queue": "adapta"},
			"future_prompt_field": "ignored"
		},
		"base_branch": "main",
		"retry_context": {
			"attempt": 2,
			"previous_reason": "review rejected: missing tests",
			"review_feedback": "add round-trip assertions",
			"previous_branch": "task/ADAPTABOT-12",
			"previous_commit_sha": "abc123",
			"future_retry_field": "ignored"
		},
		"tdd": true,
		"quality_gate": true,
		"future_payload_field": "ignored"
	}`)
	got, err = DecodeImplementPayload(withUnknown)
	if err != nil {
		t.Fatalf("decode payload with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(got, payload) {
		t.Fatalf("decoded payload mismatch:\n got: %#v\nwant: %#v", got, payload)
	}

	emptyOptionals := ImplementPayload{
		TaskID:      "ADAPTABOT-13",
		Title:       "Minimal implement item",
		Description: "No retry context.",
		PromptContext: ImplementPromptContext{
			Prompt: "Implement the task.",
		},
	}
	raw, err = json.Marshal(emptyOptionals)
	if err != nil {
		t.Fatalf("marshal empty optional payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"task_id": "ADAPTABOT-13",
		"title": "Minimal implement item",
		"description": "No retry context.",
		"prompt_context": {"prompt": "Implement the task."},
		"tdd": false,
		"quality_gate": false
	}`))

	got, err = DecodeImplementPayload(raw)
	if err != nil {
		t.Fatalf("decode empty optional payload: %v", err)
	}
	if !reflect.DeepEqual(got, emptyOptionals) {
		t.Fatalf("decoded empty optional payload mismatch:\n got: %#v\nwant: %#v", got, emptyOptionals)
	}
}

func TestImplementResultRoundTripAndEmptyOptionals(t *testing.T) {
	result := ImplementResult{
		Status:        "completed",
		Reason:        "landed",
		Branch:        "task/ADAPTABOT-12",
		CommitSHA:     "abc123",
		PRURL:         "https://example.test/pr/12",
		ReviewVerdict: "pass",
		Artifacts: map[string]string{
			"log_path": "/tmp/yolo-runner/ADAPTABOT-12.jsonl",
		},
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"status": "completed",
		"reason": "landed",
		"branch": "task/ADAPTABOT-12",
		"commit_sha": "abc123",
		"pr_url": "https://example.test/pr/12",
		"review_verdict": "pass",
		"artifacts": {"log_path": "/tmp/yolo-runner/ADAPTABOT-12.jsonl"}
	}`))

	withUnknown := []byte(`{
		"status": "completed",
		"reason": "landed",
		"branch": "task/ADAPTABOT-12",
		"commit_sha": "abc123",
		"pr_url": "https://example.test/pr/12",
		"review_verdict": "pass",
		"artifacts": {"log_path": "/tmp/yolo-runner/ADAPTABOT-12.jsonl"},
		"future_result_field": "ignored"
	}`)
	got, err := DecodeImplementResult(withUnknown)
	if err != nil {
		t.Fatalf("decode result with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(got, result) {
		t.Fatalf("decoded result mismatch:\n got: %#v\nwant: %#v", got, result)
	}

	empty := ImplementResult{Status: "blocked"}
	raw, err = json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty result: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{"status":"blocked"}`))

	got, err = DecodeImplementResult(raw)
	if err != nil {
		t.Fatalf("decode empty result: %v", err)
	}
	if !reflect.DeepEqual(got, empty) {
		t.Fatalf("decoded empty result mismatch:\n got: %#v\nwant: %#v", got, empty)
	}
}

func assertJSONEqual(t *testing.T, got []byte, want []byte) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("unmarshal got JSON %q: %v", got, err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("unmarshal want JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch:\n got: %s\nwant: %s", got, bytes.TrimSpace(want))
	}
}
