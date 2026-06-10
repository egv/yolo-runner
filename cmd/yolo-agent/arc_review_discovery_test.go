package main

import (
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
)

func TestMapEligiblePRSummariesToArcReviewDiscoveredPRs(t *testing.T) {
	prs := []arcanum.PRSummary{
		{
			ID:        "eligible-trunk",
			Reviewers: []string{"alice", "bob"},
			Branch:    "trunk",
			Status:    "open",
		},
		{
			ID:        "closed",
			Reviewers: []string{"alice"},
			Branch:    "trunk",
			Status:    "closed",
		},
		{
			ID:        "other-reviewer",
			Reviewers: []string{"bob"},
			Branch:    "trunk",
			Status:    "open",
		},
		{
			ID:        "wrong-branch",
			Reviewers: []string{"alice"},
			Branch:    "experimental",
			Status:    "open",
		},
		{
			ID:        "eligible-release",
			Reviewers: []string{"alice"},
			Branch:    "release",
			Status:    "open",
		},
	}

	tests := []struct {
		name            string
		workspace       string
		reviewer        string
		allowedBranches []string
		want            []arcReviewDiscoveredPR
	}{
		{
			name:            "keeps open assigned PRs on allowed branches and maps fields",
			workspace:       "/arcadia/users/alice/reviews",
			reviewer:        "alice",
			allowedBranches: []string{"trunk"},
			want: []arcReviewDiscoveredPR{
				{
					ID:        "eligible-trunk",
					Workspace: "/arcadia/users/alice/reviews",
					Branch:    "trunk",
				},
			},
		},
		{
			name:            "applies branch filter after reviewer and status filters",
			workspace:       "/arcadia/users/alice/reviews",
			reviewer:        "alice",
			allowedBranches: []string{"release"},
			want: []arcReviewDiscoveredPR{
				{
					ID:        "eligible-release",
					Workspace: "/arcadia/users/alice/reviews",
					Branch:    "release",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapEligiblePRSummariesToArcReviewDiscoveredPRs(tt.workspace, tt.reviewer, tt.allowedBranches, prs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mapEligiblePRSummariesToArcReviewDiscoveredPRs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
