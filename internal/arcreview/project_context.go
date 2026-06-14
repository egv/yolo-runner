package arcreview

import "context"

type ProjectContextFetcher struct {
	LinkedTicketTracker LinkedTicketTracker
}

func (f ProjectContextFetcher) FetchProjectContext(ctx context.Context, workspace string, state PRRuntimeState) (ProjectContext, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	projectContext, err := DetectProjectContext(workspace, state.ChangedFiles)
	if err != nil {
		return ProjectContext{}, err
	}

	conventions, err := ReadProjectConventions(workspace, projectContext.Root)
	if err != nil {
		return ProjectContext{}, err
	}
	projectContext.ConventionsExcerpt = conventions

	issues := projectContextTicketIssues(state)
	if len(issues) > 0 && f.LinkedTicketTracker != nil {
		tickets, err := FetchLinkedTicketSummaries(ctx, f.LinkedTicketTracker, issues)
		if err != nil {
			return ProjectContext{}, err
		}
		projectContext.LinkedTickets = tickets
	}

	return projectContext, nil
}

func projectContextTicketIssues(state PRRuntimeState) []PRIssue {
	if len(state.Details.Issues) > 0 {
		return state.Details.Issues
	}
	return state.OpenIssues
}
