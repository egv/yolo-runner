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
	{match: containsAny("git", "checkout", "branch", "rebase", "not a git repository"), class: errorClass{category: "git/vcs", remediation: "Fix repository state (clean worktree, valid branch, fetch updates) and rerun."}},
}

func FormatActionableError(err error) string {
	if err == nil {
		return ""
	}
	cause := err.Error()
	class := classifyError(cause)
	return "Category: " + class.category + "\nCause: " + cause + "\nNext step: " + class.remediation
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
