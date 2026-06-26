package arcanum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type ArcMount struct {
	Status      string
	Mount       string
	Store       string
	ObjectStore string
}

type arcMountJSON struct {
	Status      string `json:"status"`
	Mount       string `json:"mount"`
	Store       string `json:"store"`
	ObjectStore string `json:"object-store"`
}

func arcCommandError(args []string, stderr []byte, err error) error {
	command := strings.Join(append([]string{"arc"}, args...), " ")
	details := strings.TrimSpace(string(stderr))
	if details == "" {
		return fmt.Errorf("%s failed: %w", command, err)
	}
	return fmt.Errorf("%s failed: %s: %w", command, details, err)
}

// ListArcMounts performs legacy Arc CLI mount discovery. It is kept for compatibility with
// callers that still rely on workspace-based discovery and is not used by arcpr source.
func ListArcMounts(ctx context.Context) ([]ArcMount, error) {
	stdout, stderr, err := arcExec(ctx, "", "arc", "mount", "--list", "--json")
	if err != nil {
		return nil, arcCommandError([]string{"mount", "--list", "--json"}, stderr, err)
	}

	var raw []arcMountJSON
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return nil, fmt.Errorf("parse arc mount list JSON: %w", err)
	}

	mounts := make([]ArcMount, 0, len(raw))
	for _, mount := range raw {
		mounts = append(mounts, ArcMount{
			Status:      strings.TrimSpace(mount.Status),
			Mount:       strings.TrimSpace(mount.Mount),
			Store:       strings.TrimSpace(mount.Store),
			ObjectStore: strings.TrimSpace(mount.ObjectStore),
		})
	}
	return mounts, nil
}

// DefaultPRListWorkspace resolves a default mounted workspace for legacy discovery flows.
// Deprecated: arcpr source uses API-based discovery and does not need workspace mounts.
func DefaultPRListWorkspace(ctx context.Context) (string, error) {
	mounts, err := ListArcMounts(ctx)
	if err != nil {
		return "", err
	}
	for _, mount := range mounts {
		if strings.EqualFold(mount.Status, "mounted") && strings.TrimSpace(mount.Mount) != "" {
			return strings.TrimSpace(mount.Mount), nil
		}
	}
	return "", errors.New("legacy PR discovery requires a mounted Arc workspace; run `arc mount` before using workspace-based listing")
}

// ListReviewerReviewPRs lists review requests for a reviewer in a specific mounted workspace.
// Deprecated: use API-backed listing (`ListReviewPRsWithClient`) instead.
func ListReviewerReviewPRs(ctx context.Context, workspace string, reviewer string) ([]PRSummary, error) {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return nil, nil
	}
	stdout, err := RunWorkspaceArc(ctx, workspace, "pr", "list", "--json", "--reviewer", reviewer, "--status", "open")
	if err != nil {
		return nil, err
	}
	return ParsePRListJSON(stdout)
}

// ListAuthorReviewPRs lists outgoing review requests authored by `author` in a mounted workspace.
// Deprecated: use API-backed listing (`ListReviewPRsWithClient`) instead.
func ListAuthorReviewPRs(ctx context.Context, workspace string, author string) ([]PRSummary, error) {
	author = strings.TrimSpace(author)
	if author == "" {
		return nil, nil
	}
	stdout, err := RunWorkspaceArc(ctx, workspace, "pr", "list", "--json", "--author", author, "--status", "open")
	if err != nil {
		return nil, err
	}
	return ParsePRListJSON(stdout)
}

type PRListArcanumClient struct {
	apiClient *APIClient
}

func NewPRListArcanumClient(apiClient *APIClient) *PRListArcanumClient {
	return &PRListArcanumClient{apiClient: apiClient}
}

func (c *PRListArcanumClient) ListReviewerReviewPRs(ctx context.Context, reviewer string) ([]PRSummary, error) {
	return c.listReviewRequestsByFilter(ctx, "reviewer", reviewer)
}

func (c *PRListArcanumClient) ListAuthorReviewPRs(ctx context.Context, author string) ([]PRSummary, error) {
	return c.listReviewRequestsByFilter(ctx, "author", author)
}

// ListReviewPRs returns open PRs using the legacy workspace-based flow.
// Deprecated: arcpr source uses `ListReviewPRsWithClient` for API-backed discovery.
func ListReviewPRs(ctx context.Context, user string) ([]PRSummary, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil, nil
	}
	workspace, err := DefaultPRListWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	return ListReviewPRsInWorkspace(ctx, workspace, user)
}

func ListReviewPRsWithClient(ctx context.Context, apiClient *APIClient, user string) ([]PRSummary, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil, nil
	}

	client := NewPRListArcanumClient(apiClient)

	reviewerPRs, err := client.ListReviewerReviewPRs(ctx, user)
	if err != nil {
		return nil, err
	}
	authorPRs, err := client.ListAuthorReviewPRs(ctx, user)
	if err != nil {
		return nil, err
	}
	return dedupePRSummaries(reviewerPRs, authorPRs), nil
}

// ListReviewPRsInWorkspace performs legacy review-request listing for a specific workspace.
// Deprecated: use `ListReviewPRsWithClient` unless a workspace is explicitly required.
func ListReviewPRsInWorkspace(ctx context.Context, workspace string, user string) ([]PRSummary, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil, nil
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("arc PR discovery workspace is required")
	}

	reviewerPRs, err := ListReviewerReviewPRs(ctx, workspace, user)
	if err != nil {
		return nil, err
	}
	authorPRs, err := ListAuthorReviewPRs(ctx, workspace, user)
	if err != nil {
		return nil, err
	}
	return dedupePRSummaries(reviewerPRs, authorPRs), nil
}

func (c *PRListArcanumClient) listReviewRequestsByFilter(ctx context.Context, filter string, user string) ([]PRSummary, error) {
	apiClient, err := c.api()
	if err != nil {
		return nil, err
	}

	user = strings.TrimSpace(user)
	if user == "" {
		return nil, nil
	}
	if filter != "reviewer" && filter != "author" {
		return nil, fmt.Errorf("invalid review request filter %q", filter)
	}

	var raw json.RawMessage
	if err := apiClient.GetJSON(ctx, reviewRequestsPath(filter, user), &raw); err != nil {
		return nil, fmt.Errorf("list %s review requests: %w", filter, err)
	}
	return ParsePRListJSON(raw)
}

func (c *PRListArcanumClient) api() (*APIClient, error) {
	if c == nil {
		return nil, fmt.Errorf("review request list Arcanum client is nil")
	}
	if c.apiClient == nil {
		return nil, fmt.Errorf("Arcanum API client is required for review request list")
	}
	return c.apiClient, nil
}

// arcReviewRequestListLimit bounds a single review-request listing page. Arcanum
// caps projection to the requested fields, so a generous page is cheap; 1000
// matches other internal clients (bazel-steward) and covers any realistic user.
const arcReviewRequestListLimit = "1000"

// reviewRequestsPath builds the Arcanum review-request collection query.
//
// Arcanum's /v1/review-requests is not a plain GET-with-filters: it returns an
// empty {"data":{}} unless BOTH (a) the row selection is expressed in Arcanum's
// query DSL via the `query` param and (b) the columns are projected via
// `fields=review_requests(...)`. The DSL predicate selecting authored PRs is
// author(<login>); there is no reviewer(<login>) predicate, so PRs the user is
// asked to review are selected with subscriber(<login>). open() keeps only open
// review requests.
//
// active_diff_set(id) is the only per-push identifier the list API exposes (no
// commit SHA is available in list projection); it changes on every push, so it
// serves as the revision change-token used downstream for idempotency and
// re-review-on-update. vcs(from_branch,to_branch) feeds the branch fields.
func reviewRequestsPath(filter string, user string) string {
	user = strings.TrimSpace(user)
	predicate := "author"
	if filter == "reviewer" {
		predicate = "subscriber"
	}

	query := url.Values{}
	query.Set("query", predicate+"("+user+");open()")
	query.Set("fields", "review_requests(id,author,summary,status,active_diff_set(id),vcs(from_branch,to_branch))")
	query.Set("order", "-updated_at")
	query.Set("limit", arcReviewRequestListLimit)
	query.Set("offset", "0")
	return "/v1/review-requests?" + query.Encode()
}

func dedupePRSummaries(groups ...[]PRSummary) []PRSummary {
	seen := make(map[string]struct{})
	var merged []PRSummary
	for _, group := range groups {
		for _, pr := range group {
			id := strings.TrimSpace(pr.ID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			pr.ID = id
			merged = append(merged, pr)
		}
	}
	return merged
}
