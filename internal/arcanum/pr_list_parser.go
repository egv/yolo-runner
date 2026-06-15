package arcanum

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func ParsePRListJSON(data []byte) ([]PRSummary, error) {
	rawPRs, err := prListItems(data)
	if err != nil {
		return nil, err
	}

	prs := make([]PRSummary, 0, len(rawPRs))
	for i, raw := range rawPRs {
		pr, err := parsePRSummary(raw)
		if err != nil {
			return nil, fmt.Errorf("parse PR list item %d: %w", i, err)
		}
		prs = append(prs, pr)
	}
	return prs, nil
}

func prListItems(data []byte) ([]json.RawMessage, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		stream, streamErr := prListStreamItems(data)
		if streamErr == nil {
			return stream, nil
		}
		return nil, fmt.Errorf("parse arc pr list JSON: %w", err)
	}

	for _, key := range []string{"pull_requests", "pullRequests", "prs", "items", "result", "data"} {
		raw := object[key]
		if len(raw) == 0 {
			continue
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("parse %q as PR list: %w", key, err)
		}
		return list, nil
	}

	return []json.RawMessage{bytes.TrimSpace(data)}, nil
}

func prListStreamItems(data []byte) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var list []json.RawMessage

	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				return list, nil
			}
			return nil, err
		}
		list = append(list, raw)
	}
}

func parsePRSummary(raw json.RawMessage) (PRSummary, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return PRSummary{}, err
	}

	summary := firstScalar(item, "summary", "title", "name")
	fromBranch := firstScalar(item, "from_branch", "fromBranch", "source_branch", "sourceBranch")
	toBranch := firstScalar(item, "to_branch", "toBranch", "target_branch", "targetBranch", "base_branch", "baseBranch")

	return PRSummary{
		ID:         firstScalar(item, "id", "number", "pr_id", "pull_request_id"),
		Title:      summary,
		Summary:    summary,
		Author:     firstPerson(item, "author", "created_by", "createdBy"),
		Reviewers:  firstPeopleList(item, "reviewers", "reviewer_logins"),
		Branch:     firstNonEmpty(toBranch, firstScalar(item, "branch"), fromBranch),
		FromBranch: fromBranch,
		FromID:     firstScalar(item, "from_id", "fromId", "head_id", "headId", "head_commit", "headCommit", "revision", "current_revision", "currentRevision"),
		ToBranch:   toBranch,
		Status:     firstScalar(item, "status", "state"),
		Issues:     firstIssueList(item, "issues", "linked_issues", "linkedIssues"),
	}, nil
}

func firstScalar(item map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		value := scalarValue(item[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPerson(item map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		value := personValue(item[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPeopleList(item map[string]json.RawMessage, keys ...string) []string {
	for _, key := range keys {
		values := peopleListValue(item[key])
		if len(values) > 0 {
			return values
		}
	}
	return nil
}

func firstIssueList(item map[string]json.RawMessage, keys ...string) []string {
	for _, key := range keys {
		values := issueListValue(item[key])
		if len(values) > 0 {
			return values
		}
	}
	return nil
}

func peopleListValue(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var stringsList []string
	if err := json.Unmarshal(raw, &stringsList); err == nil {
		return compactStrings(stringsList)
	}

	var rawList []json.RawMessage
	if err := json.Unmarshal(raw, &rawList); err != nil {
		return nil
	}

	values := make([]string, 0, len(rawList))
	for _, item := range rawList {
		if value := personValue(item); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func issueListValue(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var stringsList []string
	if err := json.Unmarshal(raw, &stringsList); err == nil {
		return compactStrings(stringsList)
	}

	var rawList []json.RawMessage
	if err := json.Unmarshal(raw, &rawList); err != nil {
		return nil
	}

	values := make([]string, 0, len(rawList))
	for _, item := range rawList {
		if value := issueValue(item); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func personValue(raw json.RawMessage) string {
	if value := scalarValue(raw); value != "" {
		return value
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	return firstScalar(object, "login", "username", "uid", "id", "name", "display")
}

func issueValue(raw json.RawMessage) string {
	if value := scalarValue(raw); value != "" {
		return value
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	return firstScalar(object, "id", "key", "issue", "issue_id", "issueId")
}

func scalarValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return strings.TrimSpace(value)
	}

	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}

	var floatValue float64
	if err := json.Unmarshal(raw, &floatValue); err == nil {
		return strconv.FormatFloat(floatValue, 'f', -1, 64)
	}

	return ""
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
