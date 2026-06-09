package arcreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ReplyArcanumClient interface {
	PostCommentReply(ctx context.Context, prID string, commentID string, body string) error
}

type AnsweredCommentStore interface {
	ListAnsweredCommentIDs(ctx context.Context, prID string) ([]string, error)
	StoreAnsweredCommentIDs(ctx context.Context, prID string, commentIDs []string) error
}

type ReplyResult struct {
	Replies []ReviewReply `json:"replies"`
}

type ReplyApplier struct {
	Client ReplyArcanumClient
	Store  AnsweredCommentStore
}

func (a ReplyApplier) Apply(ctx context.Context, state PRRuntimeState, payload []byte) (ReplyResult, error) {
	result, err := ParseReplyResult(payload)
	if err != nil {
		return ReplyResult{}, err
	}
	if a.Client == nil {
		return ReplyResult{}, fmt.Errorf("reply Arcanum client is required")
	}
	if a.Store == nil {
		return ReplyResult{}, fmt.Errorf("answered comment store is required")
	}

	prID := currentReviewPRID(state)
	if prID == "" {
		return ReplyResult{}, fmt.Errorf("PR ID is required")
	}

	commentIDs := knownCommentIDs(state.Comments)
	answerIDs, err := a.Store.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return ReplyResult{}, fmt.Errorf("list answered comment IDs: %w", err)
	}
	answered := answeredCommentSet(answerIDs, state.Comments)

	var posted []string
	for _, reply := range result.Replies {
		commentID := strings.TrimSpace(reply.CommentID)
		body := strings.TrimSpace(reply.Body)
		if commentID == "" {
			return ReplyResult{}, fmt.Errorf("reply comment ID is required")
		}
		if body == "" {
			return ReplyResult{}, fmt.Errorf("reply body is required for comment %q", commentID)
		}
		if !commentIDs[commentID] {
			return ReplyResult{}, fmt.Errorf("reply references unknown comment %q", commentID)
		}
		if answered[commentID] {
			continue
		}
		if err := a.Client.PostCommentReply(ctx, prID, commentID, body); err != nil {
			return ReplyResult{}, fmt.Errorf("post comment reply %q: %w", commentID, err)
		}
		posted = append(posted, commentID)
		answered[commentID] = true
	}

	if len(posted) > 0 {
		if err := a.Store.StoreAnsweredCommentIDs(ctx, prID, posted); err != nil {
			return ReplyResult{}, fmt.Errorf("store answered comment IDs: %w", err)
		}
	}
	return result, nil
}

func ParseReplyResult(payload []byte) (ReplyResult, error) {
	if strings.TrimSpace(string(payload)) == "" {
		return ReplyResult{}, fmt.Errorf("reply result payload is required")
	}

	var result ReplyResult
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&result); err != nil {
		return ReplyResult{}, fmt.Errorf("parse reply result: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ReplyResult{}, fmt.Errorf("parse reply result: trailing JSON content")
	}
	return result, nil
}

func knownCommentIDs(comments []PRComment) map[string]bool {
	ids := make(map[string]bool, len(comments))
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id != "" {
			ids[id] = true
		}
	}
	return ids
}

func answeredCommentSet(answerIDs []string, comments []PRComment) map[string]bool {
	answered := make(map[string]bool, len(answerIDs)+len(comments))
	for _, id := range answerIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			answered[id] = true
		}
	}
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id != "" && comment.Answered {
			answered[id] = true
		}
	}
	return answered
}
