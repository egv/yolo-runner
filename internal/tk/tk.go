package tk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

type Runner interface {
	Run(args ...string) (string, error)
}

type Issue struct {
	ID        string  `json:"id"`
	IssueType string  `json:"issue_type"`
	Status    string  `json:"status"`
	Priority  *int    `json:"priority"`
	Children  []Issue `json:"children"`
}

type Bead struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Status             string `json:"status"`
}

type Adapter struct {
	runner Runner
}

func New(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

type ticket struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Type        string         `json:"type"`
	Priority    any            `json:"priority"`
	Parent      string         `json:"parent"`
	Deps        dependencyList `json:"deps"`
}

type dependencyList []string

func (d *dependencyList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*d = nil
		return nil
	}

	switch trimmed[0] {
	case '"':
		var raw string
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			*d = nil
			return nil
		}
		*d = parseDependencyString(raw)
		return nil
	case '[':
		var rawDeps []any
		if err := json.Unmarshal(trimmed, &rawDeps); err != nil {
			*d = nil
			return nil
		}
		deps := make(dependencyList, 0, len(rawDeps))
		for _, rawDep := range rawDeps {
			rawDepString, ok := rawDep.(string)
			if !ok {
				continue
			}
			deps = append(deps, parseDependencyString(rawDepString)...)
		}
		*d = deps
		return nil
	default:
		*d = nil
		return nil
	}
}

func parseDependencyString(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	deps := make([]string, 0, len(parts))
	for _, dep := range parts {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		deps = append(deps, dep)
	}
	return deps
}

func (a *Adapter) Ready(rootID string) (Issue, error) {
	tickets, err := a.queryTickets()
	if err != nil {
		return Issue{}, err
	}

	readyOutput, err := a.runner.Run("tk", "ready")
	if err != nil {
		return Issue{}, err
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

	var ready []Issue
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

	return Issue{ID: rootID, IssueType: "epic", Status: "open", Children: ready}, nil
}

func (a *Adapter) Tree(rootID string) (Issue, error) {
	tickets, err := a.queryTickets()
	if err != nil {
		return Issue{}, err
	}

	lookup := map[string]ticket{}
	for _, t := range tickets {
		lookup[t.ID] = t
	}

	root, ok := lookup[rootID]
	if !ok {
		return Issue{}, nil
	}

	issue := ticketToIssue(root)
	for _, t := range tickets {
		if t.Parent == rootID {
			issue.Children = append(issue.Children, ticketToIssue(t))
		}
	}

	return issue, nil
}

func (a *Adapter) Show(id string) (Bead, error) {
	showOutput, err := a.runner.Run("tk", "show", id)
	if err != nil {
		return Bead{}, err
	}
	titleFromShow := parseTitleFromShowOutput(showOutput)
	shownTicket, shownErr := parseTicketFromShowOutput(id, showOutput)

	tickets, err := a.queryTickets()
	if err != nil {
		if shownErr == nil {
			title := shownTicket.Title
			if strings.TrimSpace(title) == "" {
				title = titleFromShow
			}
			return Bead{ID: shownTicket.ID, Title: title, Description: shownTicket.Description, Status: shownTicket.Status}, nil
		}
		return Bead{}, err
	}
	for _, t := range tickets {
		if t.ID == id {
			title := t.Title
			if strings.TrimSpace(title) == "" {
				title = titleFromShow
			}
			return Bead{ID: t.ID, Title: title, Description: t.Description, Status: t.Status}, nil
		}
	}

	if shownErr == nil {
		title := shownTicket.Title
		if strings.TrimSpace(title) == "" {
			title = titleFromShow
		}
		return Bead{ID: shownTicket.ID, Title: title, Description: shownTicket.Description, Status: shownTicket.Status}, nil
	}

	return Bead{}, nil
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
	queryOutput, queryErr := a.runner.Run("tk", "query")
	if queryErr == nil {
		tickets, err := parseTicketQuery(queryOutput)
		if err == nil {
			return tickets, nil
		}
	}

	readyOutput, err := a.runner.Run("tk", "ready")
	if err != nil {
		if queryErr != nil {
			return nil, fmt.Errorf("tk query failed: %w", queryErr)
		}
		return nil, fmt.Errorf("tk ready failed: %w", err)
	}

	blockedOutput, _ := a.runner.Run("tk", "blocked")
	idSet := make(map[string]struct{})
	for _, id := range parseReadyIDs(readyOutput) {
		idSet[id] = struct{}{}
	}
	for _, id := range parseReadyIDs(blockedOutput) {
		idSet[id] = struct{}{}
	}

	result := make([]ticket, 0, len(idSet))
	for id := range idSet {
		t, err := a.fetchTicket(id)
		if err != nil {
			continue
		}
		result = append(result, t)
	}

	return result, nil
}

func (a *Adapter) fetchTicket(id string) (ticket, error) {
	output, err := a.runner.Run("tk", "show", id)
	if err != nil {
		return ticket{}, err
	}
	return parseTicketFromShowOutput(id, output)
}

func parseTicketQuery(output string) ([]ticket, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	tickets := []ticket{}
	for {
		var t ticket
		if err := decoder.Decode(&t); err != nil {
			if errors.Is(err, io.EOF) {
				return tickets, nil
			}
			return nil, err
		}
		tickets = append(tickets, t)
	}
}

func parseTicketFromShowOutput(id string, output string) (ticket, error) {
	frontmatter, found, err := extractLeadingFrontmatter(output)
	if err != nil || !found {
		return ticket{}, fmt.Errorf("no frontmatter found")
	}

	values := make(map[string]any)
	if err := yaml.Unmarshal([]byte(frontmatter), &values); err != nil {
		return ticket{}, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}

	t := ticket{ID: id}
	if v, ok := values["title"].(string); ok {
		t.Title = v
	}
	if v, ok := values["status"].(string); ok {
		t.Status = v
	}
	if v, ok := values["type"].(string); ok {
		t.Type = v
	}
	if v, ok := values["priority"]; ok {
		t.Priority = v
	}
	if v, ok := values["parent"].(string); ok {
		t.Parent = v
	}
	if v, ok := values["deps"]; ok {
		t.Deps = parseDepsField(v)
	}

	return t, nil
}

func parseDepsField(v any) dependencyList {
	switch val := v.(type) {
	case string:
		return parseDependencyString(val)
	case []any:
		deps := make(dependencyList, 0, len(val))
		for _, d := range val {
			if s, ok := d.(string); ok {
				deps = append(deps, s)
			}
		}
		return deps
	}
	return nil
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

func (a *Adapter) readyFallback(rootID string, lookup map[string]ticket) (Issue, error) {
	t, ok := lookup[rootID]
	if !ok {
		return Issue{}, nil
	}
	issue := ticketToIssue(t)
	if issue.Status != "open" {
		return Issue{}, nil
	}
	if issue.IssueType == "epic" || issue.IssueType == "molecule" {
		return Issue{}, nil
	}
	return issue, nil
}

func ticketToIssue(t ticket) Issue {
	priority := ticketPriority(t.Priority)
	return Issue{ID: t.ID, IssueType: t.Type, Status: t.Status, Priority: &priority}
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
