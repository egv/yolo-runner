package arcanum

import (
	"context"
	"errors"
	"strings"
)

type ReplyArcanumClient struct {
	Workspace string
}

func (c ReplyArcanumClient) PostCommentReply(ctx context.Context, _ string, commentID string, body string) error {
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return errors.New("arcanum comment ID is required")
	}

	_, err := RunWorkspaceArc(ctx, c.Workspace, "reply", commentID, body)
	return err
}
