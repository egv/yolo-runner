package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

type finalizePullRequestCreator interface {
	CreatePR(ctx context.Context, title string, body string) (string, error)
}

func (e *Executor) Finalize(ctx context.Context, payload workitem.FinalizePayload) (workitem.FinalizeResult, error) {
	if e == nil {
		return workitem.FinalizeResult{}, fmt.Errorf("executor is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	vcs := e.vcsForRepo(e.RepoRoot)
	prCreator, ok := vcs.(finalizePullRequestCreator)
	if !ok || prCreator == nil {
		return workitem.FinalizeResult{}, fmt.Errorf("finalize VCS does not support PR creation")
	}

	prURL, err := prCreator.CreatePR(ctx, finalizePRTitle(payload), finalizePRBody(payload))
	if err != nil {
		return workitem.FinalizeResult{}, err
	}
	prURL = strings.TrimSpace(prURL)
	if prURL == "" {
		return workitem.FinalizeResult{}, fmt.Errorf("finalize PR URL is required")
	}
	return workitem.FinalizeResult{PRURL: prURL}, nil
}

func finalizePRTitle(payload workitem.FinalizePayload) string {
	title := strings.TrimSpace(payload.Title)
	if title != "" {
		return title
	}
	parentRef := strings.TrimSpace(payload.ParentRef)
	if parentRef != "" {
		return parentRef
	}
	return "Complete split task set"
}

func finalizePRBody(payload workitem.FinalizePayload) string {
	var b strings.Builder
	if parentRef := strings.TrimSpace(payload.ParentRef); parentRef != "" {
		fmt.Fprintf(&b, "Parent: %s\n\n", parentRef)
	}
	b.WriteString("Completed split branches:")
	for _, branch := range payload.ChildBranches {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}
		fmt.Fprintf(&b, "\n- %s", branch)
	}
	return strings.TrimSpace(b.String())
}
