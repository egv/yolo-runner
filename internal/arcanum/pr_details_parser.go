package arcanum

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// ParsePRDetailsJSON parses JSON emitted by: arc pr status --json 13843457.
// Open PRs expose their current review revision as from_id; merged PRs also expose merged_revision.
func ParsePRDetailsJSON(data []byte) (arcreview.PRDetails, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(data, &item); err != nil {
		return arcreview.PRDetails{}, fmt.Errorf("parse arc pr status JSON: %w", err)
	}

	issues, err := prDetailIssues(item["issues"])
	if err != nil {
		return arcreview.PRDetails{}, err
	}

	sourceBranch := firstScalar(item, "from_branch", "fromBranch", "source_branch", "sourceBranch")
	targetBranch := firstScalar(item, "to_branch", "toBranch", "target_branch", "targetBranch", "base_branch", "baseBranch")

	return arcreview.PRDetails{
		ID:           firstScalar(item, "id", "number", "pr_id", "pull_request_id"),
		Title:        firstScalar(item, "summary", "title", "name"),
		Author:       firstPerson(item, "author", "created_by", "createdBy"),
		Branch:       firstNonEmpty(targetBranch, sourceBranch),
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		Status:       firstScalar(item, "status", "state"),
		Revision:     prDetailRevision(item),
		URL:          firstScalar(item, "url", "web_url", "webUrl"),
		Description:  firstScalar(item, "description", "body"),
		Issues:       issues,
	}, nil
}

func prDetailRevision(item map[string]json.RawMessage) string {
	return firstScalar(
		item,
		"revision",
		"current_revision",
		"currentRevision",
		"from_id",
		"fromId",
		"head_id",
		"headId",
		"merged_revision",
		"mergedRevision",
	)
}

func prDetailIssues(raw json.RawMessage) ([]arcreview.PRIssue, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var rawIssues []json.RawMessage
	if err := json.Unmarshal(raw, &rawIssues); err != nil {
		return nil, fmt.Errorf("parse arc pr status issues: %w", err)
	}

	issues := make([]arcreview.PRIssue, 0, len(rawIssues))
	for i, rawIssue := range rawIssues {
		issue, err := prDetailIssue(rawIssue)
		if err != nil {
			return nil, fmt.Errorf("parse arc pr status issue %d: %w", i, err)
		}
		if issue.ID != "" || issue.Message != "" {
			issues = append(issues, issue)
		}
	}
	return issues, nil
}

func prDetailIssue(raw json.RawMessage) (arcreview.PRIssue, error) {
	if id := scalarValue(raw); id != "" {
		return arcreview.PRIssue{ID: id, Status: "open", Message: id}, nil
	}

	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return arcreview.PRIssue{}, err
	}

	id := firstScalar(item, "id", "key", "issue", "issue_id", "issueId")
	message := firstScalar(item, "message", "summary", "title", "description")
	if message == "" {
		message = id
	}

	return arcreview.PRIssue{
		ID:       id,
		Status:   firstNonEmpty(firstScalar(item, "status", "state"), "open"),
		Severity: firstScalar(item, "severity", "level"),
		Path:     firstScalar(item, "path", "file"),
		Line:     intScalar(item, "line", "line_number", "lineNumber"),
		Message:  message,
		Author:   firstPerson(item, "author", "created_by", "createdBy"),
		Resolved: boolScalar(item, "resolved"),
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func intScalar(item map[string]json.RawMessage, keys ...string) int {
	for _, key := range keys {
		value := scalarValue(item[key])
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func boolScalar(item map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		raw := item[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value bool
		if err := json.Unmarshal(raw, &value); err == nil {
			return value
		}
	}
	return false
}
