package arcanum

import (
	"context"
)

type ReplyArcanumClient struct {
	Workspace string
}

func (c ReplyArcanumClient) PostCommentReply(ctx context.Context, _ string, commentID string, body string) error {
	_, err := RunWorkspaceArc(ctx, c.Workspace, "reply", "--comment-id", commentID, "--message", body)
	return err
}
