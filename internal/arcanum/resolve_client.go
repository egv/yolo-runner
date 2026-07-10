package arcanum

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

var _ arcreview.ResolveArcanumClient = (*ResolveArcanumClient)(nil)

// ResolveArcanumClient resolves Arcanum review comments. It mirrors
// ReplyArcanumClient so a Source must opt into a real Arcanum client; tests
// inject a fake.
type ResolveArcanumClient struct {
	apiClient *APIClient
}

// NewResolveArcanumClient wraps an APIClient for resolving review comments.
func NewResolveArcanumClient(apiClient *APIClient) *ResolveArcanumClient {
	return &ResolveArcanumClient{apiClient: apiClient}
}

// ResolveComment marks the given review comment as resolved on Arcanum. The
// prID is accepted for interface parity with ReplyArcanumClient but unused.
func (c *ResolveArcanumClient) ResolveComment(ctx context.Context, _ string, commentID string) error {
	apiClient, err := c.api()
	if err != nil {
		return err
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return fmt.Errorf("comment ID is required")
	}

	if err := apiClient.PatchJSON(ctx, reviewRequestCommentResolvePath(commentID), resolveCommentRequest{IssueStatus: "resolved"}, nil); err != nil {
		return fmt.Errorf("resolve comment: %w", err)
	}
	return nil
}

func (c *ResolveArcanumClient) api() (*APIClient, error) {
	if c == nil {
		return nil, fmt.Errorf("resolve Arcanum client is nil")
	}
	if c.apiClient == nil {
		return nil, fmt.Errorf("Arcanum API client is required")
	}
	return c.apiClient, nil
}

// reviewRequestCommentResolvePath is the live Arcanum endpoint for changing a
// review issue status. It was verified against the production API.
func reviewRequestCommentResolvePath(commentID string) string {
	return "/v1/public/review-requests-comments/" + url.PathEscape(strings.TrimSpace(commentID))
}

type resolveCommentRequest struct {
	IssueStatus string `json:"issue_status"`
}
