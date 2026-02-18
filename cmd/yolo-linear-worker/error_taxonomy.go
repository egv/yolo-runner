package main

import "strings"

type linearErrorClass struct {
	category    string
	remediation string
}

var linearSessionErrorTaxonomy = []struct {
	match func(string) bool
	class linearErrorClass
}{
	{
		match: containsAny(
			"webhook",
			"queue file",
			"decode queued job line",
			"contract version",
			"missing job identifiers",
		),
		class: linearErrorClass{
			category:    "webhook",
			remediation: "Validate webhook payload contract/version and queue records, then replay the affected event.",
		},
	},
	{
		match: containsAny(
			"linear_token",
			"linear_api_token",
			"token is required",
			"unauthorized",
			"forbidden",
			"http 401",
			"http 403",
			"bearer",
			"authorization",
			"permission denied",
		),
		class: linearErrorClass{
			category:    "auth",
			remediation: "Set a valid LINEAR_TOKEN (or LINEAR_API_TOKEN), verify workspace access, and retry.",
		},
	},
	{
		match: containsAny(
			"graphql",
			"agent activity mutation unsuccessful",
			"agent activity mutation missing activity id",
			"agent activity mutation http",
		),
		class: linearErrorClass{
			category:    "graphql",
			remediation: "Review Linear GraphQL error details and mutation input, then retry once the API issue is resolved.",
		},
	},
	{
		match: containsAny(
			"run linear session job",
			"runner timeout",
			"opencode stall",
			"deadline exceeded",
			"timed out",
		),
		class: linearErrorClass{
			category:    "runtime",
			remediation: "Inspect worker runtime logs under runner-logs/, fix the runner issue, and rerun the session step.",
		},
	},
}

func FormatLinearSessionActionableError(err error) string {
	if err == nil {
		return ""
	}
	cause := normalizeLinearErrorCause(trimLinearGenericExitStatus(err.Error()))
	class := classifyLinearSessionError(cause)
	return "Category: " + class.category + "\nCause: " + cause + "\nNext step: " + class.remediation
}

func classifyLinearSessionError(cause string) linearErrorClass {
	text := strings.ToLower(cause)
	for _, entry := range linearSessionErrorTaxonomy {
		if entry.match(text) {
			return entry.class
		}
	}
	return linearErrorClass{
		category:    "unknown",
		remediation: "Check yolo-linear-worker stderr and session logs, then retry; escalate with full error details if it persists.",
	}
}

func normalizeLinearErrorCause(cause string) string {
	parts := strings.Split(cause, "\n")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		line := strings.TrimSpace(part)
		if line == "" || isPureLinearExitStatusLine(line) {
			continue
		}
		normalized = append(normalized, line)
	}
	if len(normalized) == 0 {
		return strings.TrimSpace(cause)
	}
	return strings.Join(normalized, " | ")
}

func trimLinearGenericExitStatus(cause string) string {
	trimmed := strings.TrimSpace(cause)
	lower := strings.ToLower(trimmed)
	const suffix = ": exit status "

	idx := strings.LastIndex(lower, suffix)
	if idx == -1 {
		return trimmed
	}
	statusPart := strings.TrimSpace(trimmed[idx+len(suffix):])
	if statusPart == "" {
		return trimmed
	}
	for _, r := range statusPart {
		if r < '0' || r > '9' {
			return trimmed
		}
	}
	if idx == 0 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:idx])
}

func isPureLinearExitStatusLine(line string) bool {
	line = strings.TrimSpace(strings.ToLower(line))
	if !strings.HasPrefix(line, "exit status ") {
		return false
	}
	n := strings.TrimSpace(strings.TrimPrefix(line, "exit status "))
	if n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsAny(parts ...string) func(string) bool {
	return func(text string) bool {
		for _, part := range parts {
			if strings.Contains(text, part) {
				return true
			}
		}
		return false
	}
}
