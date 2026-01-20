package runner

type Issue struct {
	ID        string  `json:"id"`
	IssueType string  `json:"issue_type"`
	Status    string  `json:"status"`
	Priority  *int    `json:"priority"`
	Children  []Issue `json:"children"`
}

func SelectFirstOpenLeafTaskID(root Issue) string {
	if isRunnableLeaf(root) {
		if root.Status == "open" {
			return root.ID
		}
		return ""
	}
	return selectFirstOpenLeafInChildren(root.Children)
}

func selectFirstOpenLeafInChildren(children []Issue) string {
	for _, item := range sortedIssues(children) {
		if isRunnableLeaf(item) {
			if item.Status == "open" {
				return item.ID
			}
			continue
		}
		if isContainer(item) {
			if item.Status != "open" && item.Status != "in_progress" {
				continue
			}
			if len(item.Children) == 0 {
				continue
			}
			leaf := selectFirstOpenLeafInChildren(item.Children)
			if leaf != "" {
				return leaf
			}
		}
	}
	return ""
}

func isContainer(issue Issue) bool {
	return issue.IssueType == "epic" || issue.IssueType == "molecule"
}

func isRunnableLeaf(issue Issue) bool {
	if issue.IssueType == "" {
		return false
	}
	// Treat all non-container issue types as runnable leaves.
	// Containers (epic/molecule) require child traversal.
	return !isContainer(issue)
}

func CountRunnableLeaves(root Issue) int {
	if isRunnableLeaf(root) {
		return 1
	}
	if isContainer(root) {
		return countRunnableLeaves(root.Children)
	}
	return 0
}

func countRunnableLeaves(children []Issue) int {
	total := 0
	for _, item := range children {
		total += CountRunnableLeaves(item)
	}
	return total
}

func sortedIssues(items []Issue) []Issue {
	sorted := make([]Issue, len(items))
	copy(sorted, items)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if issuePriorityLess(sorted[j], sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func issuePriorityLess(a Issue, b Issue) bool {
	aPriority, aHasPriority := priorityValue(a)
	bPriority, bHasPriority := priorityValue(b)
	if aHasPriority && bHasPriority {
		return aPriority < bPriority
	}
	if aHasPriority && !bHasPriority {
		return true
	}
	if !aHasPriority && bHasPriority {
		return false
	}
	return false
}

func priorityValue(issue Issue) (int, bool) {
	if issue.Priority == nil {
		return 0, false
	}
	return *issue.Priority, true
}
