package runner

type Issue struct {
	ID        string
	IssueType string
	Status    string
	Priority  *int
	Children  []Issue
}

func SelectFirstOpenLeafTaskID(root Issue) string {
	if root.IssueType == "task" {
		if root.Status == "open" {
			return root.ID
		}
		return ""
	}
	return selectFirstOpenLeafInChildren(root.Children)
}

func selectFirstOpenLeafInChildren(children []Issue) string {
	for _, item := range sortedIssues(children) {
		if item.Status != "open" {
			continue
		}
		switch item.IssueType {
		case "task":
			return item.ID
		case "epic":
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
