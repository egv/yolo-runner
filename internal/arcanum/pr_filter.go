package arcanum

import "strings"

func FilterEligiblePRs(prs []PRSummary, reviewer string, allowedBranches []string) []PRSummary {
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return nil
	}

	branches := make(map[string]struct{}, len(allowedBranches))
	for _, branch := range allowedBranches {
		branch = strings.TrimSpace(branch)
		if branch != "" {
			branches[branch] = struct{}{}
		}
	}
	if len(branches) == 0 {
		return nil
	}

	var eligible []PRSummary
	for _, pr := range prs {
		if !isOpenPR(pr) {
			continue
		}
		if !isAssignedReviewer(pr, reviewer) {
			continue
		}
		if _, ok := branches[strings.TrimSpace(pr.Branch)]; !ok {
			continue
		}
		eligible = append(eligible, pr)
	}
	return eligible
}

func isOpenPR(pr PRSummary) bool {
	return strings.EqualFold(strings.TrimSpace(pr.Status), "open")
}

func isAssignedReviewer(pr PRSummary, reviewer string) bool {
	for _, candidate := range pr.Reviewers {
		if strings.EqualFold(strings.TrimSpace(candidate), reviewer) {
			return true
		}
	}
	return false
}
