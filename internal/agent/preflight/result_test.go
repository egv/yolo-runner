package preflight

import (
	"reflect"
	"testing"
)

func TestParseResult(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Result
	}{
		{
			name:  "ready",
			input: `{"decision":"ready","confidence":0.92,"summary":"Task is clear.","questions":[]}`,
			want: Result{
				Decision:   DecisionReady,
				Confidence: 0.92,
				Summary:    "Task is clear.",
				Questions:  []string{},
			},
		},
		{
			name:  "needs_info",
			input: `{"decision":"needs_info","confidence":0.81,"summary":"Scope is missing.","questions":["Which package owns this behavior?"]}`,
			want: Result{
				Decision:   DecisionNeedsInfo,
				Confidence: 0.81,
				Summary:    "Scope is missing.",
				Questions:  []string{"Which package owns this behavior?"},
			},
		},
		{
			name:  "low confidence",
			input: `{"decision":"ready","confidence":0.79,"summary":"Probably enough.","questions":[]}`,
			want: Result{
				Decision:   DecisionNeedsInfo,
				Confidence: 0.79,
				Summary:    "Probably enough.",
				Questions:  []string{},
			},
		},
		{
			name:  "invalid JSON",
			input: `decision=ready confidence=1`,
			want: Result{
				Decision: DecisionNeedsInfo,
			},
		},
		{
			name:  "missing decision",
			input: `{"confidence":0.95,"summary":"No explicit decision.","questions":[]}`,
			want: Result{
				Decision:   DecisionNeedsInfo,
				Confidence: 0.95,
				Summary:    "No explicit decision.",
				Questions:  []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResult(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseResult() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
