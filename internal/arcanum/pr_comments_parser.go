package arcanum

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// ParsePRCommentsJSON parses JSON emitted by: arc pr comments --json <pr-id>.
func ParsePRCommentsJSON(data []byte) ([]arcreview.PRComment, error) {
	rawComments, err := prCommentItems(data)
	if err != nil {
		return nil, err
	}

	comments := make([]arcreview.PRComment, 0, len(rawComments))
	for i, raw := range rawComments {
		parsed, err := parsePRCommentOrThread(raw)
		if err != nil {
			return nil, fmt.Errorf("parse arc pr comments item %d: %w", i, err)
		}
		comments = append(comments, parsed...)
	}
	return comments, nil
}

func prCommentItems(data []byte) ([]json.RawMessage, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("parse arc pr comments JSON: %w", err)
	}

	for _, key := range []string{"comments", "review_comments", "reviewComments", "pull_request_comments", "pullRequestComments", "threads", "items", "result"} {
		raw := object[key]
		if len(raw) == 0 {
			continue
		}
		items, err := prCommentRawList(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %q as PR comments: %w", key, err)
		}
		return items, nil
	}

	return nil, fmt.Errorf("arc pr comments JSON did not contain a comments list")
}

func prCommentRawList(raw json.RawMessage) ([]json.RawMessage, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}

	for _, key := range []string{"comments", "review_comments", "reviewComments", "pull_request_comments", "pullRequestComments", "threads", "items", "result"} {
		child := object[key]
		if len(child) == 0 {
			continue
		}
		return prCommentRawList(child)
	}

	return nil, fmt.Errorf("comments list not found")
}

func parsePRCommentOrThread(raw json.RawMessage) ([]arcreview.PRComment, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}

	comment := parsePRComment(item)
	childRaw := firstRawList(item, "comments", "replies", "answers")
	if len(childRaw) == 0 {
		return []arcreview.PRComment{comment}, nil
	}

	comments := make([]arcreview.PRComment, 0, len(childRaw)+1)
	if comment.Body != "" {
		comments = append(comments, comment)
	}

	threadID := firstNonEmpty(comment.ThreadID, comment.ID)
	for i, rawChild := range childRaw {
		var childItem map[string]json.RawMessage
		if err := json.Unmarshal(rawChild, &childItem); err != nil {
			return nil, fmt.Errorf("parse thread comment %d: %w", i, err)
		}

		child := parsePRComment(childItem)
		if child.ThreadID == "" {
			child.ThreadID = threadID
		}
		if child.Path == "" {
			child.Path = comment.Path
		}
		if child.Line == 0 {
			child.Line = comment.Line
		}
		child.Resolved = child.Resolved || comment.Resolved
		comments = append(comments, child)
	}
	return comments, nil
}

func parsePRComment(item map[string]json.RawMessage) arcreview.PRComment {
	return arcreview.PRComment{
		ID:       firstScalar(item, "id", "comment_id", "commentId", "number"),
		ThreadID: firstScalar(item, "thread_id", "threadId", "discussion_id", "discussionId"),
		Author:   firstPerson(item, "author", "created_by", "createdBy", "user"),
		Body:     firstScalar(item, "body", "text", "message", "content", "comment"),
		Path:     firstScalar(item, "path", "file", "file_path", "filePath"),
		Line:     intScalar(item, "line", "line_number", "lineNumber"),
		Revision: firstScalar(item, "revision", "from_revision", "fromRevision"),
		Resolved: prCommentBoolScalar(item, "resolved", "is_resolved", "isResolved"),
		Answered: prCommentBoolScalar(item, "answered", "is_answered", "isAnswered"),
	}
}

func firstRawList(item map[string]json.RawMessage, keys ...string) []json.RawMessage {
	for _, key := range keys {
		raw := item[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}

		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err == nil {
			return list
		}
	}
	return nil
}

func prCommentBoolScalar(item map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		raw := item[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}

		var value bool
		if err := json.Unmarshal(raw, &value); err == nil {
			return value
		}

		switch strings.ToLower(scalarValue(raw)) {
		case "1", "true", "yes", "answered", "closed", "done", "resolved":
			return true
		case "0", "false", "no", "open", "unanswered", "unresolved":
			return false
		}
	}
	return false
}
