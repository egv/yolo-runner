package arcreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ResolveArcanumClient resolves a review comment on Arcanum. Mirrors
// ReplyArcanumClient so a Source must opt into a real Arcanum client; tests
// inject a fake.
type ResolveArcanumClient interface {
	ResolveComment(ctx context.Context, prID string, commentID string) error
}

type ResolveResult struct {
	ResolvedCommentIDs []string `json:"resolved_comment_ids"`
}

type ResolveApplier struct {
	Client ResolveArcanumClient
	Store  AnsweredCommentStore
}

func (a ResolveApplier) Apply(ctx context.Context, state PRRuntimeState, payload []byte) (ResolveResult, error) {
	result, err := ParseResolveResult(payload)
	if err != nil {
		return ResolveResult{}, err
	}
	if a.Client == nil {
		return ResolveResult{}, fmt.Errorf("resolve Arcanum client is required")
	}
	if a.Store == nil {
		return ResolveResult{}, fmt.Errorf("answered comment store is required")
	}

	prID := currentReviewPRID(state)
	if prID == "" {
		return ResolveResult{}, fmt.Errorf("PR ID is required")
	}

	commentIDs := knownCommentIDs(state.Comments)
	answerIDs, err := a.Store.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("list answered comment IDs: %w", err)
	}
	handled := resolvedOrAnsweredCommentSet(answerIDs, state.Comments)

	var posted []string
	for _, commentID := range result.ResolvedCommentIDs {
		commentID = strings.TrimSpace(commentID)
		if commentID == "" {
			return ResolveResult{}, fmt.Errorf("resolve comment ID is required")
		}
		if !commentIDs[commentID] {
			return ResolveResult{}, fmt.Errorf("resolve references unknown comment %q", commentID)
		}
		if handled[commentID] {
			continue
		}
		if err := a.Client.ResolveComment(ctx, prID, commentID); err != nil {
			return ResolveResult{}, fmt.Errorf("resolve comment %q: %w", commentID, err)
		}
		posted = append(posted, commentID)
		handled[commentID] = true
	}

	if len(posted) > 0 {
		if err := a.Store.StoreAnsweredCommentIDs(ctx, prID, posted); err != nil {
			return ResolveResult{}, fmt.Errorf("store answered comment IDs: %w", err)
		}
	}
	return result, nil
}

func ParseResolveResult(payload []byte) (ResolveResult, error) {
	if strings.TrimSpace(string(payload)) == "" {
		return ResolveResult{}, fmt.Errorf("resolve result payload is required")
	}

	var result ResolveResult
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&result); err != nil {
		return ResolveResult{}, fmt.Errorf("parse resolve result: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ResolveResult{}, fmt.Errorf("parse resolve result: trailing JSON content")
	}
	return result, nil
}

// resolvedOrAnsweredCommentSet returns the set of comment IDs that are already
// resolved or answered: persisted answered IDs, comments marked Answered, and
// comments marked Resolved. Reuses answeredCommentSet and folds in Resolved so
// the reply applier's semantics stay untouched.
func resolvedOrAnsweredCommentSet(answerIDs []string, comments []PRComment) map[string]bool {
	handled := answeredCommentSet(answerIDs, comments)
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id != "" && comment.Resolved {
			handled[id] = true
		}
	}
	return handled
}
