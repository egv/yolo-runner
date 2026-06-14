package arcreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
)

type LinkedTicketTracker interface {
	GetIssue(ctx context.Context, issueID string) (trackerstartrek.Issue, error)
}

type LinkedTicketSummary struct {
	ID                 string `json:"id"`
	Title              string `json:"title,omitempty"`
	Status             string `json:"status,omitempty"`
	Intent             string `json:"intent,omitempty"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
}

func FetchLinkedTicketSummaries(ctx context.Context, tracker LinkedTicketTracker, issues []PRIssue) ([]LinkedTicketSummary, error) {
	keys := linkedTicketIssueKeys(issues)
	if len(keys) == 0 {
		return nil, nil
	}
	if tracker == nil {
		return nil, errors.New("linked ticket tracker is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	summaries := make([]LinkedTicketSummary, 0, len(keys))
	for _, key := range keys {
		issue, err := tracker.GetIssue(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("fetch linked Startrek ticket %q: %w", key, err)
		}
		summaries = append(summaries, summarizeLinkedTicket(issue, key))
	}
	return summaries, nil
}

func linkedTicketIssueKeys(issues []PRIssue) []string {
	if len(issues) == 0 {
		return nil
	}
	keys := make([]string, 0, len(issues))
	for _, issue := range issues {
		key := strings.TrimSpace(issue.ID)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func summarizeLinkedTicket(issue trackerstartrek.Issue, fallbackID string) LinkedTicketSummary {
	description := strings.TrimSpace(issue.Description)
	sections := linkedTicketDescriptionSections(description)

	return LinkedTicketSummary{
		ID:                 firstNonEmptyString(issue.ID, fallbackID),
		Title:              strings.TrimSpace(issue.Title),
		Status:             strings.TrimSpace(issue.Status),
		Intent:             compactLinkedTicketText(firstNonEmptyString(sections["intent"], linkedTicketLead(description))),
		AcceptanceCriteria: compactLinkedTicketMultiline(firstNonEmptyString(sections["acceptance_criteria"], sections["done_when"])),
	}
}

func linkedTicketDescriptionSections(description string) map[string]string {
	sections := map[string]string{}
	var current string
	var currentLines []string

	flush := func() {
		if current == "" {
			currentLines = nil
			return
		}
		value := compactLinkedTicketMultiline(strings.Join(currentLines, "\n"))
		if value != "" {
			sections[current] = value
		}
		currentLines = nil
	}

	for _, line := range strings.Split(normalizeLinkedTicketNewlines(description), "\n") {
		if heading, rest, ok := linkedTicketHeading(line); ok {
			flush()
			current = heading
			if rest != "" {
				currentLines = append(currentLines, rest)
			}
			continue
		}
		if current != "" {
			currentLines = append(currentLines, line)
		}
	}
	flush()

	return sections
}

func linkedTicketHeading(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return "", "", false
	}

	markdownHeading := strings.HasPrefix(trimmed, "#")
	if markdownHeading {
		trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	}

	name := trimmed
	rest := ""
	if idx := strings.Index(trimmed, ":"); idx >= 0 {
		name = strings.TrimSpace(trimmed[:idx])
		rest = strings.TrimSpace(trimmed[idx+1:])
	} else if !markdownHeading {
		return "", "", false
	}

	if name == "" || len([]rune(name)) > 80 || !looksLikeLinkedTicketHeadingName(name) {
		return "", "", false
	}

	switch normalizeLinkedTicketHeadingName(name) {
	case "intent", "goal", "why", "context":
		return "intent", rest, true
	case "acceptance criteria", "acceptance", "ac", "definition of done":
		return "acceptance_criteria", rest, true
	case "done when":
		return "done_when", rest, true
	default:
		if rest == "" {
			return "", "", true
		}
		return "", "", false
	}
}

func linkedTicketLead(description string) string {
	var lines []string
	for _, line := range strings.Split(normalizeLinkedTicketNewlines(description), "\n") {
		if _, _, ok := linkedTicketHeading(line); ok {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
}

func normalizeLinkedTicketHeadingName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return strings.Join(strings.Fields(name), " ")
}

func looksLikeLinkedTicketHeadingName(name string) bool {
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || r == '_' || r == '-' || r == '/' {
			continue
		}
		return false
	}
	return true
}

func compactLinkedTicketText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func compactLinkedTicketMultiline(value string) string {
	var lines []string
	for _, line := range strings.Split(normalizeLinkedTicketNewlines(value), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeLinkedTicketNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
