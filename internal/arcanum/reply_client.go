package arcanum

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type ReplyArcanumClient struct {
	Workspace string
}

func (c ReplyArcanumClient) PostCommentReply(ctx context.Context, _ string, commentID string, body string) error {
	tokenOutput, err := RunWorkspaceArc(ctx, c.Workspace, "token", "show")
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(tokenOutput))
	if token == "" {
		return fmt.Errorf("arc token show in workspace %s returned empty token", c.Workspace)
	}

	payload, err := json.Marshal(replyArcanumComment{
		Content: body,
		Draft:   false,
	})
	if err != nil {
		return fmt.Errorf("marshal Arcanum reply comment: %w", err)
	}

	endpoint := "https://arcanum.yandex.net/api/v1/public/review-requests-comments/" + url.PathEscape(commentID) + "/replies"
	args := []string{
		"--fail-with-body",
		"--silent",
		"--show-error",
		"--request", "POST",
		"--header", "Authorization: OAuth " + token,
		"--header", "Content-Type: application/json",
		"--data-binary", string(payload),
		endpoint,
	}
	stdout, stderr, err := arcExec(
		ctx,
		c.Workspace,
		"curl",
		args...,
	)
	if err != nil {
		return replyWorkspaceCommandError(c.Workspace, "curl", args, stdout, stderr, err)
	}
	return nil
}

type replyArcanumComment struct {
	Content string `json:"content"`
	Draft   bool   `json:"draft"`
}

func replyWorkspaceCommandError(workspace string, name string, args []string, stdout []byte, stderr []byte, err error) error {
	command := strings.Join(append([]string{name}, redactReplyCommandArgs(name, args)...), " ")
	details := strings.TrimSpace(strings.Join(nonemptyReplyCommandOutputs(stdout, stderr), ": "))
	if details == "" {
		return fmt.Errorf("%s in workspace %s failed: %w", command, workspace, err)
	}
	return fmt.Errorf("%s in workspace %s failed: %s: %w", command, workspace, details, err)
}

func redactReplyCommandArgs(name string, args []string) []string {
	out := append([]string{}, args...)
	if name != "curl" {
		return out
	}
	for i, arg := range out {
		if strings.HasPrefix(arg, "Authorization: OAuth ") {
			out[i] = "Authorization: OAuth <redacted>"
		}
	}
	return out
}

func nonemptyReplyCommandOutputs(stdout []byte, stderr []byte) []string {
	out := []string{}
	for _, output := range [][]byte{stdout, stderr} {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
