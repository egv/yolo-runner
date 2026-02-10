package agent

import "strings"

type errorClass struct {
	category    string
	remediation string
}

var errorTaxonomy = []struct {
	match func(string) bool
	class errorClass
}{
	{match: containsAny("merge conflict", "non-fast-forward", "merge queue"), class: errorClass{category: "merge_queue_conflict", remediation: "Sync main, rebase the task branch, resolve conflicts, then retry landing."}},
	{match: containsAny("review rejected", "verification not confirmed", "failing acceptance criteria"), class: errorClass{category: "review_gating", remediation: "Address review feedback, rerun implementation, and re-run review mode."}},
	{match: containsAny("opencode stall", "runner timeout", "deadline exceeded", "timed out"), class: errorClass{category: "runner_timeout_stall", remediation: "Inspect runner and opencode logs, increase --runner-timeout if needed, then rerun."}},
	{match: containsAny("serena initialization failed", "yolo agent missing", "permission: allow", "yolo-runner init"), class: errorClass{category: "runner_init", remediation: "Run yolo-runner init, verify .opencode/agent/yolo.md, and confirm required config exists."}},
	{match: containsAny("auth", "token", "credential", "profile", "permission denied", "config"), class: errorClass{category: "auth_profile_config", remediation: "Verify auth/profile/config values, refresh credentials, and retry with the correct profile."}},
	{match: containsAny("chdir", "no such file", "repository does not exist", "clone"), class: errorClass{category: "filesystem_clone", remediation: "Confirm repository path exists, clone/fetch repository data, and retry from repo root."}},
	{match: containsAny("task lock", "already locked", "resource busy", "lock held"), class: errorClass{category: "lock_contention", remediation: "Wait for other workers to finish or release stale lock, then retry."}},
	{match: containsAny("tk ", "ticket", "task tracker", ".tickets"), class: errorClass{category: "tracker", remediation: "Verify tk CLI availability and task metadata, then rerun task selection."}},
	{match: containsAny("git", "checkout", "branch", "rebase", "not a git repository", "worktree", "dirty", "local changes", "would be overwritten by checkout"), class: errorClass{category: "git/vcs", remediation: "Fix repository state (clean worktree, valid branch, fetch updates) and rerun."}},
}

func FormatActionableError(err error) string {
	if err == nil {
		return ""
	}
	cause := normalizeCause(trimGenericExitStatus(err.Error()))
	class := classifyError(cause)
	return "Category: " + class.category + "\nCause: " + cause + "\nNext step: " + class.remediation
}

func normalizeCause(cause string) string {
	parts := strings.Split(cause, "\n")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		line := strings.TrimSpace(part)
		if line == "" || isPureExitStatusLine(line) {
			continue
		}
		normalized = append(normalized, line)
	}
	if len(normalized) == 0 {
		return strings.TrimSpace(cause)
	}
	return strings.Join(normalized, " | ")
}

func isPureExitStatusLine(line string) bool {
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

func trimGenericExitStatus(cause string) string {
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

func classifyError(cause string) errorClass {
	text := strings.ToLower(cause)
	for _, entry := range errorTaxonomy {
		if entry.match(text) {
			return entry.class
		}
	}
	return errorClass{
		category:    "unknown",
		remediation: "Check runner logs for details and retry; escalate with full error text if it persists.",
	}
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
