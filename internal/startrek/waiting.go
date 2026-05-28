package startrek

import (
	"strings"
	"time"
)

// NeedsInfoWaitState carries the issue participants and marker timestamp needed
// to decide whether a blocked needs-info issue can resume.
type NeedsInfoWaitState struct {
	MarkerCreatedAt time.Time
	Author          IssueAuthor
	Assignee        IssueAuthor
	AgentAuthorIDs  []string
}

// ShouldResumeNeedsInfoWait reports whether a newer non-agent comment from the
// issue author or assignee arrived after the needs-info marker comment.
func ShouldResumeNeedsInfoWait(wait NeedsInfoWaitState, comments []IssueComment) bool {
	if wait.MarkerCreatedAt.IsZero() {
		return false
	}

	humanAuthorIDs := map[string]struct{}{}
	addAuthorID(humanAuthorIDs, wait.Author.ID)
	addAuthorID(humanAuthorIDs, wait.Assignee.ID)
	if len(humanAuthorIDs) == 0 {
		return false
	}

	agentAuthorIDs := map[string]struct{}{}
	for _, id := range wait.AgentAuthorIDs {
		addAuthorID(agentAuthorIDs, id)
	}

	for _, comment := range comments {
		if strings.TrimSpace(comment.Body) == "" || !comment.CreatedAt.After(wait.MarkerCreatedAt) {
			continue
		}

		authorID := normalizeAuthorID(comment.Author.ID)
		if authorID == "" {
			continue
		}
		if _, ok := agentAuthorIDs[authorID]; ok {
			continue
		}
		if _, ok := humanAuthorIDs[authorID]; ok {
			return true
		}
	}

	return false
}

func addAuthorID(ids map[string]struct{}, id string) {
	if normalized := normalizeAuthorID(id); normalized != "" {
		ids[normalized] = struct{}{}
	}
}

func normalizeAuthorID(id string) string {
	return strings.TrimSpace(id)
}
