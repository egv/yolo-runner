package main

import "github.com/egv/yolo-runner/v2/internal/arcanum"

func mapEligiblePRSummariesToArcReviewDiscoveredPRs(workspace string, reviewer string, allowedBranches []string, prs []arcanum.PRSummary) []arcReviewDiscoveredPR {
	eligible := arcanum.FilterEligiblePRs(prs, reviewer, allowedBranches)
	if len(eligible) == 0 {
		return nil
	}

	discovered := make([]arcReviewDiscoveredPR, 0, len(eligible))
	for _, pr := range eligible {
		discovered = append(discovered, arcReviewDiscoveredPR{
			ID:        pr.ID,
			Workspace: workspace,
			Branch:    pr.Branch,
		})
	}
	return discovered
}
