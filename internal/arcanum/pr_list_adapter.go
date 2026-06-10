package arcanum

import "context"

func ListWorkspacePRs(ctx context.Context, workspace string) ([]PRSummary, error) {
	stdout, err := RunWorkspaceArc(ctx, workspace, "pr", "list", "--json")
	if err != nil {
		return nil, err
	}
	return ParsePRListJSON(stdout)
}
