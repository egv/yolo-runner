package arcreview

import "strings"

// WithDisclosureFooter returns body followed by the agent-authored disclosure
// footer for author. The footer is separated from the body by exactly one blank
// line and is appended at most once, so passing the helper its own output is a
// no-op (idempotent).
func WithDisclosureFooter(body, author string) string {
	body = strings.TrimRight(body, " \t\r\n")
	if body == "" {
		body = "(no content)"
	}
	footer := formatDisclosureFooter(author)
	if strings.HasSuffix(body, footer) {
		return body
	}
	return body + "\n\n" + footer
}

// formatDisclosureFooter renders the disclosure line. The system appends it; the
// model prompt must never include it directly.
func formatDisclosureFooter(author string) string {
	return "_Posted on behalf of @" + normalizeAuthorHandle(author) + " by yolo-agent_"
}

// normalizeAuthorHandle trims surrounding whitespace and a single leading "@"
// so the handle renders cleanly inside the "@<author>" placeholder.
func normalizeAuthorHandle(author string) string {
	author = strings.TrimSpace(author)
	author = strings.TrimPrefix(author, "@")
	return author
}
