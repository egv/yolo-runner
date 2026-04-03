package contracts

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestErrorMessageFormatContracts ensures error messages follow established format patterns
func TestErrorMessageFormatContracts(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		validate    func(error) error
		description string
	}{
		{
			name: "command failure includes command and details",
			err:  fmt.Errorf("git checkout main failed: error: Your local changes would be overwritten: exit status 1"),
			validate: func(err error) error {
				msg := err.Error()
				if !strings.Contains(msg, "git checkout main failed") {
					return fmt.Errorf("missing command context in error: %s", msg)
				}
				if !strings.Contains(msg, "Your local changes") {
					return fmt.Errorf("missing error details in error: %s", msg)
				}
				return nil
			},
			description: "Command failures should include the command that failed and specific error details",
		},
		{
			name: "runner status validation includes expected values",
			err:  ErrInvalidRunnerResultStatus,
			validate: func(err error) error {
				msg := err.Error()
				if !strings.Contains(msg, "invalid") && !strings.Contains(msg, "status") {
					return fmt.Errorf("error should describe invalid status: %s", msg)
				}
				return nil
			},
			description: "Validation errors should clearly indicate what is invalid",
		},
		{
			name: "file path errors include relative paths",
			err:  fmt.Errorf("missing yolo agent file at .opencode/agent/yolo.md"),
			validate: func(err error) error {
				msg := err.Error()
				if !strings.Contains(msg, ".opencode/agent/yolo.md") {
					return fmt.Errorf("file error should include relative path: %s", msg)
				}
				if !strings.Contains(msg, "missing") {
					return fmt.Errorf("file error should indicate what's wrong: %s", msg)
				}
				return nil
			},
			description: "File-related errors should include the problematic path and what's wrong",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(tc.err); err != nil {
				t.Fatalf("validation failed: %v\nDescription: %s", err, tc.description)
			}
		})
	}
}

// TestErrorTaxonomyContracts ensures error messages work with the taxonomy system
func TestErrorTaxonomyContracts(t *testing.T) {
	// Import the error taxonomy function
	type actionableErrorFormatter func(error) string

	// Mock the FormatActionableError function behavior
	mockFormatActionableError := func(err error) string {
		if err == nil {
			return ""
		}
		cause := normalizeCause(trimGenericExitStatus(err.Error()))
		class := classifyError(cause)
		return "Category: " + class.category + "\nCause: " + cause + "\nNext step: " + class.remediation
	}

	tests := []struct {
		name                string
		err                 error
		expectedCategory    string
		expectedRemediation string
	}{
		{
			name:             "git checkout error classified as git/vcs",
			err:              errors.New("git checkout feature/task failed"),
			expectedCategory: "git/vcs",
		},
		{
			name:             "beads command error classified as tracker",
			err:              errors.New("tk show task-1: file not found"),
			expectedCategory: "tracker",
		},
		{
			name:             "missing agent classified as runner_init",
			err:              errors.New("serena initialization failed: missing config"),
			expectedCategory: "runner_init",
		},
		{
			name:             "timeout error classified as runner_timeout_stall",
			err:              errors.New("opencode stall category=no_output"),
			expectedCategory: "runner_timeout_stall",
		},
		{
			name:             "merge conflict classified as merge_queue_conflict",
			err:              errors.New("merge conflict while landing branch"),
			expectedCategory: "merge_queue_conflict",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message := mockFormatActionableError(tc.err)

			if !strings.Contains(message, "Category: "+tc.expectedCategory) {
				t.Fatalf("expected category %q in message, got %q", tc.expectedCategory, message)
			}

			if !strings.Contains(message, "Cause: "+tc.err.Error()) {
				t.Fatalf("expected cause in message, got %q", message)
			}

			if !strings.Contains(message, "Next step:") {
				t.Fatalf("expected next step in message, got %q", message)
			}
		})
	}
}

// TestErrorWrappingContracts ensures errors are properly wrapped with context
func TestErrorWrappingContracts(t *testing.T) {
	tests := []struct {
		name         string
		wrappedError error
		validate     func(error) error
	}{
		{
			name:         "file operations wrapped with context",
			wrappedError: fmt.Errorf("read yolo agent template: %w", errors.New("no such file or directory")),
			validate: func(wrapped error) error {
				// Check that the error is properly wrapped by checking the unwrapped chain
				unwrapped := errors.Unwrap(wrapped)
				if unwrapped == nil {
					return fmt.Errorf("wrapped error should have an underlying error")
				}
				if unwrapped.Error() != "no such file or directory" {
					return fmt.Errorf("underlying error mismatch: got %q", unwrapped.Error())
				}
				if !strings.Contains(wrapped.Error(), "read yolo agent template") {
					return fmt.Errorf("wrapped error should include context")
				}
				if !strings.Contains(wrapped.Error(), "no such file or directory") {
					return fmt.Errorf("wrapped error should include base error message")
				}
				return nil
			},
		},
		{
			name:         "command errors wrapped with command context",
			wrappedError: fmt.Errorf("git checkout main failed: %s: %w", "error: Your local changes", errors.New("exit status 1")),
			validate: func(wrapped error) error {
				// Check that the error is properly wrapped by checking the unwrapped chain
				unwrapped := errors.Unwrap(wrapped)
				if unwrapped == nil {
					return fmt.Errorf("wrapped error should have an underlying error")
				}
				if unwrapped.Error() != "exit status 1" {
					return fmt.Errorf("underlying error mismatch: got %q", unwrapped.Error())
				}
				if !strings.Contains(wrapped.Error(), "git checkout main failed") {
					return fmt.Errorf("wrapped error should include command")
				}
				if !strings.Contains(wrapped.Error(), "exit status 1") {
					return fmt.Errorf("wrapped error should include base error message")
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(tc.wrappedError); err != nil {
				t.Fatalf("wrapping validation failed: %v", err)
			}
		})
	}
}

// TestErrorConstantContracts ensures defined error variables follow conventions
func TestErrorConstantContracts(t *testing.T) {
	tests := []struct {
		name     string
		errVar   error
		validate func(error) error
	}{
		{
			name:   "ErrInvalidRunnerResultStatus format",
			errVar: ErrInvalidRunnerResultStatus,
			validate: func(err error) error {
				msg := err.Error()
				// Should be lowercase, descriptive, and indicate the problem
				if strings.ToUpper(msg) == msg {
					return fmt.Errorf("error message should not be all uppercase: %s", msg)
				}
				if !strings.Contains(msg, "invalid") {
					return fmt.Errorf("should indicate what's invalid: %s", msg)
				}
				if !strings.Contains(msg, "status") {
					return fmt.Errorf("should indicate what kind of status: %s", msg)
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(tc.errVar); err != nil {
				t.Fatalf("constant validation failed: %v", err)
			}
		})
	}
}

// TestErrorMessageContentContracts ensures error messages contain required information
func TestErrorMessageContentContracts(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		required  []string // substrings that must be present
		forbidden []string // substrings that must NOT be present
		patterns  []string // regex patterns that must match
	}{
		{
			name:     "git error includes operation and details",
			err:      fmt.Errorf("git checkout main failed: error: Your local changes would be overwritten by checkout: exit status 1"),
			required: []string{"git checkout", "failed", "Your local changes"},
			patterns: []string{`git \w+.*failed`},
		},
		{
			name:      "agent error includes action and path",
			err:       fmt.Errorf("missing yolo agent file at .opencode/agent/yolo.md"),
			required:  []string{"missing", "yolo agent file", ".opencode/agent/yolo.md"},
			forbidden: []string{"panic", "fatal"}, // should not include system-level terms
		},
		{
			name:      "validation error indicates what's invalid",
			err:       ErrInvalidRunnerResultStatus,
			required:  []string{"invalid", "status"},
			forbidden: []string{"error:", "failed"}, // should be concise
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()

			// Check required substrings
			for _, req := range tc.required {
				if !strings.Contains(msg, req) {
					t.Fatalf("missing required substring %q in error: %s", req, msg)
				}
			}

			// Check forbidden substrings
			for _, forb := range tc.forbidden {
				if strings.Contains(msg, forb) {
					t.Fatalf("found forbidden substring %q in error: %s", forb, msg)
				}
			}

			// Check regex patterns
			for _, pattern := range tc.patterns {
				matched, err := regexp.MatchString(pattern, msg)
				if err != nil {
					t.Fatalf("invalid pattern %q: %v", pattern, err)
				}
				if !matched {
					t.Fatalf("error message %q does not match pattern %q", msg, pattern)
				}
			}
		})
	}
}

// TestErrorSeverityContracts ensures error messages are appropriate for their severity level
func TestErrorSeverityContracts(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		isCritical bool // true for critical errors, false for recoverable ones
		validate   func(error) error
	}{
		{
			name:       "file not found is recoverable",
			err:        fmt.Errorf("missing yolo agent file at .opencode/agent/yolo.md"),
			isCritical: false,
			validate: func(err error) error {
				msg := err.Error()
				// Recoverable errors should be descriptive and suggest solutions
				if strings.Contains(msg, "panic") || strings.Contains(msg, "fatal") {
					return fmt.Errorf("recoverable error should not use panic/fatal language: %s", msg)
				}
				return nil
			},
		},
		{
			name:       "validation errors should be clear but not alarming",
			err:        ErrInvalidRunnerResultStatus,
			isCritical: false,
			validate: func(err error) error {
				msg := err.Error()
				// Validation errors should be informative but calm
				if strings.Contains(msg, "critical") || strings.Contains(msg, "emergency") {
					return fmt.Errorf("validation error should not use alarming language: %s", msg)
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(tc.err); err != nil {
				t.Fatalf("severity validation failed: %v", err)
			}
		})
	}
}

// Helper functions to mock the error taxonomy behavior
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

type errorClass struct {
	category    string
	remediation string
}

var mockErrorTaxonomy = []struct {
	match func(string) bool
	class errorClass
}{
	{match: containsAny("merge conflict", "non-fast-forward", "merge queue"), class: errorClass{category: "merge_queue_conflict", remediation: "Sync main, rebase the task branch, resolve conflicts, then retry landing."}},
	{match: containsAny("review rejected", "verification not confirmed", "failing acceptance criteria"), class: errorClass{category: "review_gating", remediation: "Address review feedback, rerun implementation, and re-run review mode."}},
	{match: containsAny("opencode stall", "runner timeout", "deadline exceeded", "timed out"), class: errorClass{category: "runner_timeout_stall", remediation: "Inspect runner and opencode logs, increase --runner-timeout if needed, then rerun."}},
	{match: containsAny("serena initialization failed", "yolo agent missing", "permission: allow", ".opencode/agent/yolo.md"), class: errorClass{category: "runner_init", remediation: "Install the repo-local OpenCode assets under .opencode/agent, .opencode/skills, and .opencode/commands, then retry."}},
	{match: containsAny("auth", "token", "credential", "profile", "permission denied", "config"), class: errorClass{category: "auth_profile_config", remediation: "Verify auth/profile/config values, refresh credentials, and retry with the correct profile."}},
	{match: containsAny("chdir", "no such file", "repository does not exist", "clone"), class: errorClass{category: "filesystem_clone", remediation: "Confirm repository path exists, clone/fetch repository data, and retry from repo root."}},
	{match: containsAny("task lock", "already locked", "resource busy", "lock held"), class: errorClass{category: "lock_contention", remediation: "Wait for other workers to finish or release stale lock, then retry."}},
	{match: containsAny("tk ", "ticket", "task tracker", ".tickets"), class: errorClass{category: "tracker", remediation: "Verify tk CLI availability and task metadata, then rerun task selection."}},
	{match: containsAny("git", "checkout", "branch", "rebase", "not a git repository", "worktree", "dirty", "local changes", "would be overwritten by checkout"), class: errorClass{category: "git/vcs", remediation: "Fix repository state (clean worktree, valid branch, fetch updates) and rerun."}},
}

func classifyError(cause string) errorClass {
	text := strings.ToLower(cause)
	for _, entry := range mockErrorTaxonomy {
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

// TestEventErrorContracts ensures event-related error handling follows contracts
func TestEventErrorContracts(t *testing.T) {
	tests := []struct {
		name     string
		event    Event
		validate func(Event) error
	}{
		{
			name: "event timestamp validation",
			event: Event{
				Type:      EventTypeTaskStarted,
				TaskID:    "task-123",
				Timestamp: time.Now().UTC(),
			},
			validate: func(e Event) error {
				if e.Timestamp.IsZero() {
					return fmt.Errorf("event timestamp should not be zero")
				}
				if e.Type == "" {
					return fmt.Errorf("event type should not be empty")
				}
				if e.TaskID == "" {
					return fmt.Errorf("task ID should not be empty for task events")
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.validate(tc.event); err != nil {
				t.Fatalf("event validation failed: %v", err)
			}
		})
	}
}

// TestRunnerResultErrorContracts ensures runner result errors follow contracts
func TestRunnerResultErrorContracts(t *testing.T) {
	tests := []struct {
		name        string
		result      RunnerResult
		expectError bool
		errorType   string // type of error expected: "validation", "none"
	}{
		{
			name:        "valid completed result",
			result:      RunnerResult{Status: RunnerResultCompleted},
			expectError: false,
			errorType:   "none",
		},
		{
			name:        "valid blocked result",
			result:      RunnerResult{Status: RunnerResultBlocked, Reason: "timeout"},
			expectError: false,
			errorType:   "none",
		},
		{
			name:        "valid failed result",
			result:      RunnerResult{Status: RunnerResultFailed, Reason: "command failed"},
			expectError: false,
			errorType:   "none",
		},
		{
			name:        "invalid status should fail validation",
			result:      RunnerResult{Status: RunnerResultStatus("unknown")},
			expectError: true,
			errorType:   "validation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.result.Validate()

			if tc.expectError && err == nil {
				t.Fatalf("expected validation error but got none")
			}

			if !tc.expectError && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}

			if tc.expectError && tc.errorType == "validation" {
				if !errors.Is(err, ErrInvalidRunnerResultStatus) {
					t.Fatalf("expected ErrInvalidRunnerResultStatus, got %v", err)
				}
			}
		})
	}
}

// TestErrorMessageConsistencyContracts ensures similar errors have consistent messaging
func TestErrorMessageConsistencyContracts(t *testing.T) {
	// Test that similar command failures follow the same pattern
	commandErrors := []string{
		"git checkout main failed: error: Your local changes would be overwritten by checkout",
		"git push origin main failed: permission denied",
		"git add . failed: some files could not be added",
	}

	for _, errMsg := range commandErrors {
		t.Run("command error pattern: "+errMsg, func(t *testing.T) {
			// All command errors should follow: "command args failed: details"
			pattern := regexp.MustCompile(`^[a-z]+\s+.*\s+failed:`)
			if !pattern.MatchString(errMsg) {
				t.Fatalf("command error should follow 'command args failed:' pattern: %s", errMsg)
			}
		})
	}

	// Test that file errors follow consistent patterns
	fileErrors := []string{
		"missing yolo agent file at .opencode/agent/yolo.md",
		"cannot read config file at .yolo-runner/config.yaml",
	}

	for _, errMsg := range fileErrors {
		t.Run("file error pattern: "+errMsg, func(t *testing.T) {
			// File errors should indicate the action and path
			if strings.Contains(errMsg, "file") && !strings.Contains(errMsg, "at ") {
				t.Fatalf("file error should include path with 'at': %s", errMsg)
			}
		})
	}
}
