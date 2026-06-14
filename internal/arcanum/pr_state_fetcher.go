package arcanum

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

const arcanumPublicAPIBaseURL = "https://a.yandex-team.ru/api/v1/public"

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

	checks, err := ParsePRChecksJSON(detailsOutput)
	if err != nil {
		return arcreview.PRRuntimeState{}, fmt.Errorf("parse PR checks: %w", err)
	}

	commentsOutput, err := fetchArcanumPRComments(ctx, workspace, prID)
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

	return arcreview.NormalizePRRuntimeState(details, comments, changedFiles, checks), nil
}

func FetchPRComments(ctx context.Context, prID string) ([]arcreview.PRComment, error) {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return nil, fmt.Errorf("PR ID is required")
	}

	commentsOutput, err := fetchArcanumPRComments(ctx, "", prID)
	if err != nil {
		return nil, err
	}
	comments, err := ParsePRCommentsJSON(commentsOutput)
	if err != nil {
		return nil, fmt.Errorf("parse PR comments: %w", err)
	}
	return comments, nil
}

func fetchArcanumPRComments(ctx context.Context, workspace string, prID string) ([]byte, error) {
	return runWorkspaceCommand(ctx, workspace, "curl", "-fsSL", arcanumPRCommentsURL(prID))
}

func arcanumPRCommentsURL(prID string) string {
	return arcanumPublicAPIBaseURL + "/review-requests/" + url.PathEscape(prID) + "/comments"
}

func runWorkspaceCommand(ctx context.Context, workspace string, name string, args ...string) ([]byte, error) {
	stdout, stderr, err := arcExec(ctx, workspace, name, args...)
	if err != nil {
		return nil, workspaceCommandError(name, workspace, args, stderr, err)
	}
	return stdout, nil
}

func workspaceCommandError(name string, workspace string, args []string, stderr []byte, err error) error {
	command := strings.Join(append([]string{name}, args...), " ")
	details := strings.TrimSpace(string(stderr))
	if details == "" {
		return fmt.Errorf("%s in workspace %s failed: %w", command, workspace, err)
	}
	return fmt.Errorf("%s in workspace %s failed: %s: %w", command, workspace, details, err)
}
