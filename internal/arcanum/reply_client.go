package arcanum

import "context"

type ReplyArcanumClient struct {
	Workspace string
}

func (c ReplyArcanumClient) PostCommentReply(ctx context.Context, prID string, commentID string, body string) error {
	_, err := RunWorkspaceArc(
		ctx,
		c.Workspace,
		"reply",
		"--pr-id", prID,
		"--comment-id", commentID,
		"--message", body,
	)
	return err
}
