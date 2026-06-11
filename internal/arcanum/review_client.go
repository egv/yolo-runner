package arcanum

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

const (
	reviewRequestDiffSetFields = "active_diff_set(id,gsid,status,patch_vcs_ids(source_commit_id,arc_branch_heads(from_id,to_id,merge_id))),diff_sets(id,gsid,status,patch_vcs_ids(source_commit_id,arc_branch_heads(from_id,to_id,merge_id)))"
	diffSetChangelistFields    = "path,entry_id"
)

var _ arcreview.ReviewArcanumClient = (*ReviewArcanumClient)(nil)

type ReviewArcanumClient struct {
	apiClient *APIClient
}

func NewReviewArcanumClient(apiClient *APIClient) *ReviewArcanumClient {
	return &ReviewArcanumClient{apiClient: apiClient}
}

func (c *ReviewArcanumClient) PostReviewSummary(ctx context.Context, prID string, revision string, body string) error {
	apiClient, err := c.api()
	if err != nil {
		return err
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("review summary body is required")
	}

	request := createReviewRequestCommentRequest{Content: body}
	if err := apiClient.PostJSON(ctx, reviewRequestCommentsPath(prID), request, nil); err != nil {
		return fmt.Errorf("post review summary: %w", err)
	}
	return nil
}

func (c *ReviewArcanumClient) PostReviewInlineComment(ctx context.Context, prID string, revision string, comment arcreview.ReviewInlineComment) error {
	apiClient, err := c.api()
	if err != nil {
		return err
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	if strings.TrimSpace(comment.Body) == "" {
		return fmt.Errorf("review inline comment body is required")
	}

	request := createReviewRequestCommentRequest{Content: comment.Body}
	anchor, err := c.resolveInlineCommentAnchor(ctx, prID, revision, comment)
	if err != nil {
		return fmt.Errorf("resolve inline comment anchor: %w", err)
	}
	if anchor.EntryID == "" {
		request.Content = fallbackInlineCommentContent(comment)
	} else {
		request.EntryID = anchor.EntryID
		request.DiffSide = "new"
		request.DiffLine = comment.Line
		request.DiffSetXID = anchor.DiffSetXID
	}

	if err := apiClient.PostJSON(ctx, reviewRequestCommentsPath(prID), request, nil); err != nil {
		return fmt.Errorf("post review inline comment: %w", err)
	}
	return nil
}

func (c *ReviewArcanumClient) api() (*APIClient, error) {
	if c == nil {
		return nil, fmt.Errorf("review Arcanum client is nil")
	}
	if c.apiClient == nil {
		return nil, fmt.Errorf("Arcanum API client is required")
	}
	return c.apiClient, nil
}

func (c *ReviewArcanumClient) resolveInlineCommentAnchor(ctx context.Context, prID string, revision string, comment arcreview.ReviewInlineComment) (reviewInlineCommentAnchor, error) {
	path := normalizeReviewCommentPath(comment.Path)
	if path == "" || comment.Line <= 0 {
		return reviewInlineCommentAnchor{}, nil
	}

	diffSet, err := c.resolveReviewDiffSet(ctx, prID, revision)
	if err != nil {
		return reviewInlineCommentAnchor{}, err
	}
	diffSetXID := diffSet.xid()
	if diffSetXID == "" {
		return reviewInlineCommentAnchor{}, nil
	}

	entryID, err := c.resolveDiffSetEntryID(ctx, prID, diffSetXID, path)
	if err != nil {
		return reviewInlineCommentAnchor{}, err
	}
	if entryID == "" {
		return reviewInlineCommentAnchor{DiffSetXID: diffSetXID}, nil
	}
	return reviewInlineCommentAnchor{
		EntryID:    entryID,
		DiffSetXID: diffSetXID,
	}, nil
}

func (c *ReviewArcanumClient) resolveReviewDiffSet(ctx context.Context, prID string, revision string) (reviewDiffSet, error) {
	apiClient, err := c.api()
	if err != nil {
		return reviewDiffSet{}, err
	}

	var response reviewRequestResponse
	if err := apiClient.GetJSON(ctx, reviewRequestDiffSetsPath(prID), &response); err != nil {
		return reviewDiffSet{}, err
	}

	revision = strings.TrimSpace(revision)
	candidates := response.Data.diffSetCandidates()
	if revision != "" {
		for _, diffSet := range candidates {
			if diffSet.matchesRevision(revision) {
				return diffSet, nil
			}
		}
	}
	for _, diffSet := range candidates {
		if strings.EqualFold(strings.TrimSpace(diffSet.Status), "published") {
			return diffSet, nil
		}
	}
	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return reviewDiffSet{}, nil
}

func (c *ReviewArcanumClient) resolveDiffSetEntryID(ctx context.Context, prID string, diffSetXID string, path string) (string, error) {
	apiClient, err := c.api()
	if err != nil {
		return "", err
	}

	var response diffSetChangelistResponse
	if err := apiClient.GetJSON(ctx, diffSetChangelistPath(prID, diffSetXID), &response); err != nil {
		return "", err
	}

	path = normalizeReviewCommentPath(path)
	for _, change := range response.Data {
		if normalizeReviewCommentPath(change.Path) == path {
			return strings.TrimSpace(change.EntryID), nil
		}
	}
	return "", nil
}

func reviewRequestCommentsPath(prID string) string {
	return "/v1/review-requests/" + url.PathEscape(strings.TrimSpace(prID)) + "/comments"
}

func reviewRequestDiffSetsPath(prID string) string {
	return "/v1/review-requests/" + url.PathEscape(strings.TrimSpace(prID)) + "?fields=" + url.QueryEscape(reviewRequestDiffSetFields)
}

func diffSetChangelistPath(prID string, diffSetXID string) string {
	return "/v1/review-requests/" + url.PathEscape(strings.TrimSpace(prID)) +
		"/diff-sets/" + url.PathEscape(strings.TrimSpace(diffSetXID)) +
		"/changelist?fields=" + url.QueryEscape(diffSetChangelistFields)
}

func fallbackInlineCommentContent(comment arcreview.ReviewInlineComment) string {
	path := normalizeReviewCommentPath(comment.Path)
	body := comment.Body
	if path == "" || comment.Line <= 0 {
		return body
	}
	return fmt.Sprintf("%s:%d: %s", path, comment.Line, body)
}

func normalizeReviewCommentPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	return path
}

type createReviewRequestCommentRequest struct {
	Content    string `json:"content"`
	EntryID    string `json:"entry_id,omitempty"`
	DiffSide   string `json:"diff_side,omitempty"`
	DiffLine   int    `json:"diff_line,omitempty"`
	DiffSetXID string `json:"diff_set_xid,omitempty"`
}

type reviewInlineCommentAnchor struct {
	EntryID    string
	DiffSetXID string
}

type reviewRequestResponse struct {
	Data reviewRequestData `json:"data"`
}

type reviewRequestData struct {
	ActiveDiffSet reviewDiffSet   `json:"active_diff_set"`
	DiffSets      []reviewDiffSet `json:"diff_sets"`
}

func (d reviewRequestData) diffSetCandidates() []reviewDiffSet {
	candidates := make([]reviewDiffSet, 0, len(d.DiffSets)+1)
	if d.ActiveDiffSet.xid() != "" {
		candidates = append(candidates, d.ActiveDiffSet)
	}
	candidates = append(candidates, d.DiffSets...)
	return candidates
}

type reviewDiffSet struct {
	ID          jsonScalarString `json:"id"`
	GSID        string           `json:"gsid"`
	Status      string           `json:"status"`
	PatchVCSIDs patchVCSIDs      `json:"patch_vcs_ids"`
}

func (d reviewDiffSet) xid() string {
	return firstNonEmpty(strings.TrimSpace(string(d.ID)), strings.TrimSpace(d.GSID))
}

func (d reviewDiffSet) matchesRevision(revision string) bool {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return false
	}

	for _, value := range []string{
		string(d.ID),
		d.GSID,
		d.PatchVCSIDs.SourceCommitID,
		d.PatchVCSIDs.ArcBranchHeads.FromID,
		d.PatchVCSIDs.ArcBranchHeads.ToID,
		d.PatchVCSIDs.ArcBranchHeads.MergeID,
	} {
		if strings.TrimSpace(value) == revision {
			return true
		}
	}
	return false
}

type patchVCSIDs struct {
	SourceCommitID string         `json:"source_commit_id"`
	ArcBranchHeads arcBranchHeads `json:"arc_branch_heads"`
}

type arcBranchHeads struct {
	FromID  string `json:"from_id"`
	ToID    string `json:"to_id"`
	MergeID string `json:"merge_id"`
}

type diffSetChangelistResponse struct {
	Data []diffSetChange `json:"data"`
}

type diffSetChange struct {
	Path    string `json:"path"`
	EntryID string `json:"entry_id"`
}

type jsonScalarString string

func (s *jsonScalarString) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))
	if value == "" || value == "null" {
		*s = ""
		return nil
	}
	if strings.HasPrefix(value, "\"") {
		unquoted, err := strconvUnquote(value)
		if err != nil {
			return err
		}
		*s = jsonScalarString(unquoted)
		return nil
	}
	*s = jsonScalarString(value)
	return nil
}

func strconvUnquote(value string) (string, error) {
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("decode JSON string scalar: %w", err)
	}
	return unquoted, nil
}
