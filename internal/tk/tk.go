package tk

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

type Runner interface {
	Run(args ...string) (string, error)
}

type Adapter struct {
	runner Runner
}

func New(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

type ticket struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Type        string   `json:"type"`
	Priority    any      `json:"priority"`
	Parent      string   `json:"parent"`
	Deps        []string `json:"deps"`
}

func (a *Adapter) Ready(rootID string) (runner.Issue, error) {
	tickets, err := a.queryTickets()
	if err != nil {
		return runner.Issue{}, err
	}

	readyOutput, err := a.runner.Run("tk", "ready")
	if err != nil {
		return runner.Issue{}, err
	}
	readyIDs := parseReadyIDs(readyOutput)

	blockedIDs := map[string]struct{}{}
	blockedOutput, err := a.runner.Run("tk", "blocked")
	if err == nil {
		for _, id := range parseReadyIDs(blockedOutput) {
			blockedIDs[id] = struct{}{}
		}
	}

	lookup := map[string]ticket{}
	for _, t := range tickets {
		lookup[t.ID] = t
	}

	var ready []runner.Issue
	for _, id := range readyIDs {
		if _, blocked := blockedIDs[id]; blocked {
			continue
		}
		t, ok := lookup[id]
		if !ok {
			continue
		}
		if t.Type == "epic" || t.Type == "molecule" {
			continue
		}
		if !isDescendantOrSelf(id, rootID, lookup) {
			continue
		}
		ready = append(ready, ticketToIssue(t))
	}

	if len(ready) == 0 {
		return a.readyFallback(rootID, lookup)
	}
	if len(ready) == 1 {
		return ready[0], nil
	}

	return runner.Issue{ID: rootID, IssueType: "epic", Status: "open", Children: ready}, nil
}

func (a *Adapter) Tree(rootID string) (runner.Issue, error) {
	tickets, err := a.queryTickets()
	if err != nil {
		return runner.Issue{}, err
	}

	lookup := map[string]ticket{}
	for _, t := range tickets {
		lookup[t.ID] = t
	}

	root, ok := lookup[rootID]
	if !ok {
		return runner.Issue{}, nil
	}

	issue := ticketToIssue(root)
	for _, t := range tickets {
		if t.Parent == rootID {
			issue.Children = append(issue.Children, ticketToIssue(t))
		}
	}

	return issue, nil
}

func (a *Adapter) Show(id string) (runner.Bead, error) {
	// Keep an explicit tk show call for detail retrieval flow and parity with CLI behavior.
	showOutput, err := a.runner.Run("tk", "show", id)
	if err != nil {
		return runner.Bead{}, err
	}
	titleFromShow := parseTitleFromShowOutput(showOutput)

	tickets, err := a.queryTickets()
	if err != nil {
		return runner.Bead{}, err
	}
	for _, t := range tickets {
		if t.ID == id {
			title := t.Title
			if strings.TrimSpace(title) == "" {
				title = titleFromShow
			}
			return runner.Bead{
				ID:                 t.ID,
				Title:              title,
				Description:        t.Description,
				AcceptanceCriteria: "",
				Status:             t.Status,
			}, nil
		}
	}

	return runner.Bead{}, nil
}

func parseTitleFromShowOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

func (a *Adapter) UpdateStatus(id string, status string) error {
	cmd := []string{"tk", "status", id, status}
	switch status {
	case "closed":
		cmd = []string{"tk", "close", id}
	case "open":
		cmd = []string{"tk", "reopen", id}
	case "in_progress":
		cmd = []string{"tk", "start", id}
	case "blocked", "failed":
		cmd = []string{"tk", "status", id, "open"}
	}
	_, err := a.runner.Run(cmd...)
	return err
}

func (a *Adapter) UpdateStatusWithReason(id string, status string, reason string) error {
	if err := a.UpdateStatus(id, status); err != nil {
		return err
	}

	sanitized := sanitizeReason(reason)
	if sanitized == "" {
		return nil
	}
	if status == "blocked" || status == "failed" {
		sanitized = status + ": " + sanitized
	}

	_, err := a.runner.Run("tk", "add-note", id, sanitized)
	return err
}

func (a *Adapter) Close(id string) error {
	_, err := a.runner.Run("tk", "close", id)
	return err
}

func (a *Adapter) CloseEligible() error {
	return nil
}

func (a *Adapter) Sync() error {
	return nil
}

func (a *Adapter) queryTickets() ([]ticket, error) {
	output, err := a.runner.Run("tk", "query")
	if err != nil {
		return nil, err
	}

	return parseTicketQuery(output)
}

func parseTicketQuery(output string) ([]ticket, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	result := make([]ticket, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var t ticket
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}

func parseReadyIDs(output string) []string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		ids = append(ids, fields[0])
	}
	return ids
}

func isDescendantOrSelf(id string, rootID string, lookup map[string]ticket) bool {
	if id == rootID {
		return true
	}
	current, ok := lookup[id]
	if !ok {
		return false
	}
	parent := current.Parent
	for parent != "" {
		if parent == rootID {
			return true
		}
		next, exists := lookup[parent]
		if !exists {
			break
		}
		parent = next.Parent
	}
	return false
}

func (a *Adapter) readyFallback(rootID string, lookup map[string]ticket) (runner.Issue, error) {
	t, ok := lookup[rootID]
	if !ok {
		return runner.Issue{}, nil
	}
	issue := ticketToIssue(t)
	if issue.Status != "open" {
		return runner.Issue{}, nil
	}
	if issue.IssueType == "epic" || issue.IssueType == "molecule" {
		return runner.Issue{}, nil
	}
	return issue, nil
}

func ticketToIssue(t ticket) runner.Issue {
	priority := ticketPriority(t.Priority)
	return runner.Issue{
		ID:        t.ID,
		IssueType: t.Type,
		Status:    t.Status,
		Priority:  &priority,
	}
}

func ticketPriority(raw any) int {
	switch value := raw.(type) {
	case float64:
		return int(value)
	case string:
		if value == "" {
			return 0
		}
		result := 0
		for _, ch := range value {
			if ch < '0' || ch > '9' {
				return result
			}
			result = result*10 + int(ch-'0')
		}
		return result
	default:
		return 0
	}
}

func sanitizeReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\r", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\n", "; ")
	const maxLen = 500
	if len(trimmed) > maxLen {
		return truncateRunes(trimmed, maxLen)
	}
	return trimmed
}

func truncateRunes(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range input {
		if count == maxRunes {
			return input[:i]
		}
		count++
	}
	return input
}

func IsAvailable() bool {
	if _, err := os.Stat(".tickets"); err == nil {
		return true
	}
	_, err := exec.LookPath("tk")
	return err == nil
}
