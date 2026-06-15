package arcanum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return "", errors.New("no mounted Arc workspace found for PR discovery; run `arc mount` before starting the Arc PR source")
}

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

// ListReviewPRs returns open PRs the configured user should monitor: PRs where
// that user is an assigned reviewer and PRs authored by that user, deduplicated
// by PR ID with reviewer entries taking precedence.
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
