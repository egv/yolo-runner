package arcreview

import (
	"strings"
	"time"
)

type PRRuntimeState struct {
	PRID         string          `json:"pr_id"`
	Revision     string          `json:"revision"`
	Details      PRDetails       `json:"details"`
	Comments     []PRComment     `json:"comments"`
	OpenIssues   []PRIssue       `json:"open_issues"`
	ChangedFiles []PRChangedFile `json:"changed_files"`
	Checks       []PRCheck       `json:"checks"`
}

type PRDetails struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Author       string    `json:"author"`
	Branch       string    `json:"branch"`
	SourceBranch string    `json:"source_branch,omitempty"`
	TargetBranch string    `json:"target_branch,omitempty"`
	Status       string    `json:"status"`
	Revision     string    `json:"revision"`
	URL          string    `json:"url,omitempty"`
	Description  string    `json:"description,omitempty"`
	Issues       []PRIssue `json:"issues,omitempty"`
}

type PRComment struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	Path      string    `json:"path,omitempty"`
	Line      int       `json:"line,omitempty"`
	Revision  string    `json:"revision,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Resolved  bool      `json:"resolved,omitempty"`
	Answered  bool      `json:"answered,omitempty"`
}

type PRIssue struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Severity string `json:"severity,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
	Author   string `json:"author,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

type PRChangedFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
	Diff      string `json:"diff,omitempty"`
}

type PRCheck struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Summary     string    `json:"summary,omitempty"`
	URL         string    `json:"url,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

func NormalizePRRuntimeState(details PRDetails, comments []PRComment, changedFiles []PRChangedFile, checks []PRCheck) PRRuntimeState {
	return PRRuntimeState{
		PRID:         details.ID,
		Revision:     details.Revision,
		Details:      cloneDetails(details),
		Comments:     cloneComments(comments),
		OpenIssues:   openIssues(details.Issues),
		ChangedFiles: cloneChangedFiles(changedFiles),
		Checks:       cloneChecks(checks),
	}
}

func cloneDetails(details PRDetails) PRDetails {
	details.Issues = cloneIssues(details.Issues)
	return details
}

func openIssues(issues []PRIssue) []PRIssue {
	if issues == nil {
		return nil
	}
	out := make([]PRIssue, 0, len(issues))
	for _, issue := range issues {
		if isOpenIssue(issue) {
			out = append(out, issue)
		}
	}
	return out
}

func isOpenIssue(issue PRIssue) bool {
	if issue.Resolved {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(issue.Status)) {
	case "closed", "done", "fixed", "resolved":
		return false
	default:
		return true
	}
}

func cloneIssues(issues []PRIssue) []PRIssue {
	if issues == nil {
		return nil
	}
	out := make([]PRIssue, len(issues))
	copy(out, issues)
	return out
}

func cloneComments(comments []PRComment) []PRComment {
	if comments == nil {
		return nil
	}
	out := make([]PRComment, len(comments))
	copy(out, comments)
	return out
}

func cloneChangedFiles(files []PRChangedFile) []PRChangedFile {
	if files == nil {
		return nil
	}
	out := make([]PRChangedFile, len(files))
	copy(out, files)
	return out
}

func cloneChecks(checks []PRCheck) []PRCheck {
	if checks == nil {
		return nil
	}
	out := make([]PRCheck, len(checks))
	copy(out, checks)
	return out
}
