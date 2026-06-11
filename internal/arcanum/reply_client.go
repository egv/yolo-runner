package arcanum

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

var _ arcreview.ReplyArcanumClient = (*ReplyArcanumClient)(nil)

type ReplyArcanumClient struct {
	apiClient *APIClient
}

func NewReplyArcanumClient(apiClient *APIClient) *ReplyArcanumClient {
	return &ReplyArcanumClient{apiClient: apiClient}
}

func (c *ReplyArcanumClient) PostCommentReply(ctx context.Context, _ string, commentID string, body string) error {
	apiClient, err := c.api()
	if err != nil {
		return err
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return fmt.Errorf("comment ID is required")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("comment reply body is required")
	}

	request := createCommentReplyRequest{Content: body}
	if err := apiClient.PostJSON(ctx, reviewRequestCommentRepliesPath(commentID), request, nil); err != nil {
		return fmt.Errorf("post comment reply: %w", err)
	}
	return nil
}

func (c *ReplyArcanumClient) api() (*APIClient, error) {
	if c == nil {
		return nil, fmt.Errorf("reply Arcanum client is nil")
	}
	if c.apiClient == nil {
		return nil, fmt.Errorf("Arcanum API client is required")
	}
	return c.apiClient, nil
}

func reviewRequestCommentRepliesPath(commentID string) string {
	return "/v1/review-requests-comments/" + url.PathEscape(strings.TrimSpace(commentID)) + "/replies"
}

type createCommentReplyRequest struct {
	Content string `json:"content"`
}
