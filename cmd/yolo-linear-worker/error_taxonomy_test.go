package main

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatLinearSessionActionableErrorClassifiesCommonFailureModes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
	}{
		{
			name:     "webhook",
			err:      errors.New("decode queued job line: unsupported queued job contract version 2"),
			category: "webhook",
		},
		{
			name:     "auth",
			err:      errors.New("LINEAR_TOKEN is required"),
			category: "auth",
		},
		{
			name:     "graphql",
			err:      errors.New("agent activity mutation graphql errors: rate limited"),
			category: "graphql",
		},
		{
			name:     "runtime",
			err:      errors.New("run linear session job: opencode stall category=no_output"),
			category: "runtime",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatLinearSessionActionableError(tc.err)
			if !strings.Contains(got, "Category: "+tc.category) {
				t.Fatalf("expected category %q, got %q", tc.category, got)
			}
			if !strings.Contains(got, "Cause: "+tc.err.Error()) {
				t.Fatalf("expected cause %q, got %q", tc.err.Error(), got)
			}
			if !strings.Contains(got, "Next step:") {
				t.Fatalf("expected remediation guidance, got %q", got)
			}
		})
	}
}
