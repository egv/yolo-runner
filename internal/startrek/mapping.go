package startrek

import (
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type TaskMappingOptions struct {
	QueueKey string
	RootID   string
}

func MapIssueToTask(issue Issue, comments []IssueComment, opts TaskMappingOptions) contracts.Task {
	id := strings.TrimSpace(issue.ID)
	title := fallbackText(issue.Title, id)
	rootID := strings.TrimSpace(opts.RootID)
	queueKey := fallbackText(opts.QueueKey, deriveQueueKey(id))

	return contracts.Task{
		ID:          id,
		Title:       title,
		Description: buildTaskDescription(issue, comments, title, queueKey, rootID),
		Status:      taskStatusFromIssueStatus(issue.Status),
		ParentID:    rootID,
	}
}

func buildTaskDescription(issue Issue, comments []IssueComment, title string, queueKey string, rootID string) string {
	var b strings.Builder

	writeField(&b, "Title", title)
	writeField(&b, "Issue", strings.TrimSpace(issue.ID))
	writeField(&b, "Queue", queueKey)
	writeField(&b, "Root", rootID)
	writeField(&b, "Author", formatAuthor(issue.Author))
	writeField(&b, "Labels", formatLabels(issue.Labels))

	b.WriteString("\nDescription:\n")
	description := strings.TrimSpace(issue.Description)
	if description == "" {
		b.WriteString("None")
	} else {
		b.WriteString(description)
	}

	b.WriteString("\n\nRecent comments:\n")
	wroteComment := false
	for _, comment := range comments {
		body := strings.TrimSpace(comment.Body)
		if body == "" {
			continue
		}
		if wroteComment {
			b.WriteByte('\n')
		}
		b.WriteString(formatComment(comment, body))
		wroteComment = true
	}
	if !wroteComment {
		b.WriteString("None")
	}

	return b.String()
}

func writeField(b *strings.Builder, name string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "None"
	}
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func formatLabels(labels []string) string {
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label != "" {
			normalized = append(normalized, label)
		}
	}
	if len(normalized) == 0 {
		return ""
	}
	return strings.Join(normalized, ", ")
}

func formatAuthor(author IssueAuthor) string {
	display := strings.TrimSpace(author.Display)
	id := strings.TrimSpace(author.ID)
	switch {
	case display != "" && id != "":
		return display + " (" + id + ")"
	case display != "":
		return display
	case id != "":
		return id
	default:
		return ""
	}
}

func formatComment(comment IssueComment, body string) string {
	var b strings.Builder
	if !comment.CreatedAt.IsZero() {
		b.WriteString(comment.CreatedAt.UTC().Format(time.RFC3339))
		b.WriteString(" - ")
	}
	b.WriteString(formatAuthor(comment.Author))
	b.WriteString(": ")
	b.WriteString(body)
	return b.String()
}

func deriveQueueKey(issueID string) string {
	index := strings.Index(issueID, "-")
	if index <= 0 {
		return ""
	}
	return issueID[:index]
}

func fallbackText(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return strings.TrimSpace(fallback)
	}
	return trimmed
}
