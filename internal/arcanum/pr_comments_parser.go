package arcanum

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// ParsePRCommentsJSON parses JSON from Arcanum:
// GET https://a.yandex-team.ru/api/v1/public/review-requests/{id}/comments.
//
// The installed arc CLI exposes no comments subcommand; `arc pr status --json`
// and `arc pr history --json` do not include review comments.
func ParsePRCommentsJSON(data []byte) ([]arcreview.PRComment, error) {
	rawComments, err := prCommentItems(data)
	if err != nil {
		return nil, err
	}

	comments := make([]arcreview.PRComment, 0, len(rawComments))
	for i, raw := range rawComments {
		comment, err := parseArcanumPRComment(raw)
		if err != nil {
			return nil, fmt.Errorf("parse Arcanum PR comment %d: %w", i, err)
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

func prCommentItems(data []byte) ([]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("parse Arcanum PR comments JSON: %w", err)
	}

	raw := object["data"]
	if len(raw) == 0 {
		return nil, fmt.Errorf("Arcanum PR comments JSON did not contain data")
	}

	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse data as PR comments: %w", err)
	}
	return list, nil
}

func parseArcanumPRComment(raw json.RawMessage) (arcreview.PRComment, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return arcreview.PRComment{}, err
	}

	createdAt, err := timeScalar(item, "created_at", "createdAt")
	if err != nil {
		return arcreview.PRComment{}, err
	}
	updatedAt, err := timeScalar(item, "updated_at", "updatedAt")
	if err != nil {
		return arcreview.PRComment{}, err
	}

	issueStatus := normalizedPRCommentIssueStatus(item)
	comment := arcreview.PRComment{
		ID:        firstScalar(item, "id"),
		ThreadID:  firstScalar(item, "reply_to_id", "replyToId"),
		Author:    firstPerson(item, "user", "author"),
		Body:      firstScalar(item, "content"),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Resolved:  issueStatus == "resolved",
		Answered:  prCommentIsAnswered(item, issueStatus),
	}
	applyPRCommentAnchor(&comment, item)

	return comment, nil
}

func normalizedPRCommentIssueStatus(item map[string]json.RawMessage) string {
	status := firstScalar(item, "issue_status", "issueStatus")
	return strings.ToLower(strings.TrimSpace(status))
}

func prCommentIsAnswered(item map[string]json.RawMessage, issueStatus string) bool {
	if firstScalar(item, "reply_to_id", "replyToId") != "" {
		return true
	}
	if boolScalar(item, "draft", "is_draft", "isDraft") {
		return true
	}
	if firstScalar(item, "deleted_at", "deletedAt") != "" {
		return true
	}

	switch issueStatus {
	case "open", "resolved":
		return false
	default:
		return true
	}
}

func timeScalar(item map[string]json.RawMessage, keys ...string) (time.Time, error) {
	for _, key := range keys {
		value := firstScalar(item, key)
		if value == "" {
			continue
		}

		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse %s %q: %w", key, value, err)
		}
		return parsed, nil
	}
	return time.Time{}, nil
}

func applyPRCommentAnchor(comment *arcreview.PRComment, item map[string]json.RawMessage) {
	applyPRCommentAnchorRaw(comment, item["anchor"])
	if comment.Path == "" || comment.Line == 0 || comment.Revision == "" {
		applyPRCommentAnchorRaw(comment, item["original_anchor"])
	}
}

func applyPRCommentAnchorRaw(comment *arcreview.PRComment, raw json.RawMessage) {
	anchor := rawObject(raw)
	if anchor == nil {
		return
	}

	reviewRequest := rawObject(anchor["review_request"])
	diff := rawObject(reviewRequest["diff"])
	if diff == nil {
		return
	}

	if comment.Revision == "" {
		comment.Revision = firstScalar(diff, "diff_set_xid", "diffSetXid")
	}

	file := rawObject(diff["file"])
	if file == nil {
		return
	}
	if comment.Path == "" {
		comment.Path = prCommentFilePath(file)
	}
	if comment.Line == 0 {
		position := rawObject(file["position"])
		comment.Line = intScalar(position, "line")
	}
}

func prCommentFilePath(file map[string]json.RawMessage) string {
	if path := firstScalar(file, "path", "file_path", "filePath"); path != "" {
		return path
	}

	entryID := rawObject(file["entry_id"])
	for _, key := range []string{"content_id_after", "contentIdAfter", "content_id_before", "contentIdBefore"} {
		contentID := rawObject(entryID[key])
		if path := firstScalar(contentID, "path"); path != "" {
			return path
		}
	}
	return ""
}

func rawObject(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	return object
}
