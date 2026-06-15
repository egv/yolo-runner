package arcreview

import "testing"

func TestParseReviewResultExtractsJSONFromNoisyOutput(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantSummary string
	}{
		{
			name:        "bare object",
			payload:     `{"summary":"looks good","ship":{"verdict":"ship"}}`,
			wantSummary: "looks good",
		},
		{
			name:        "markdown fenced",
			payload:     "```json\n{\"summary\":\"fenced review\"}\n```",
			wantSummary: "fenced review",
		},
		{
			name:        "cyrillic preamble then object",
			payload:     "Вот мой обзор:\n{\"summary\":\"review after prose\"}\n",
			wantSummary: "review after prose",
		},
		{
			name:        "trailing prose after object",
			payload:     "{\"summary\":\"review then signoff\"}\n\nНадеюсь, это поможет.",
			wantSummary: "review then signoff",
		},
		{
			name:        "brace inside string value",
			payload:     `prefix {"summary":"handles { and } in text","ship":{"verdict":"comment"}} suffix`,
			wantSummary: "handles { and } in text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseReviewResult([]byte(tc.payload))
			if err != nil {
				t.Fatalf("ParseReviewResult() error = %v", err)
			}
			if result.Summary != tc.wantSummary {
				t.Fatalf("summary = %q, want %q", result.Summary, tc.wantSummary)
			}
		})
	}
}

func TestParseReviewResultErrors(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{name: "empty", payload: "   "},
		{name: "no json", payload: "no object here at all"},
		{name: "object without summary", payload: `{"ship":{"verdict":"ship"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseReviewResult([]byte(tc.payload)); err == nil {
				t.Fatalf("ParseReviewResult() error = nil, want error")
			}
		})
	}
}
