package arcanum

import (
	"context"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return arcreview.PRRuntimeState{}, fmt.Errorf("workspace is required")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return arcreview.PRRuntimeState{}, fmt.Errorf("PR ID is required")
	}

	detailsOutput, err := RunWorkspaceArc(ctx, workspace, "pr", "status", "--json", prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	details, err := ParsePRDetailsJSON(detailsOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR details: %w", err)
	}

	commentsOutput, err := RunWorkspaceArc(ctx, workspace, "pr", "comments", "--json", prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	comments, err := ParsePRCommentsJSON(commentsOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR comments: %w", err)
	}

	diffOutput, err := RunWorkspaceArc(ctx, workspace, "pr", "changes", prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	changedFiles, err := ParsePRChangedFilesDiff(diffOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR changed files: %w", err)
	}

	checksOutput, err := RunWorkspaceArc(ctx, workspace, "pr", "checks", "--json", prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	checks, err := ParsePRChecksJSON(checksOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR checks: %w", err)
	}

	return arcreview.NormalizePRRuntimeState(details, comments, changedFiles, checks), nil
}
