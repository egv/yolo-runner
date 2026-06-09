package arcanum

import (
	"reflect"
	"testing"
)

func TestFilterEligiblePRs(t *testing.T) {
	prs := []PRSummary{
		{
			ID:        "eligible",
			Reviewers: []string{"alice", "bob"},
			Branch:    "trunk",
			Status:    "open",
		},
		{
			ID:        "unassigned",
			Reviewers: []string{"bob"},
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
			ID:        "wrong-branch",
			Reviewers: []string{"alice"},
			Branch:    "release",
			Status:    "open",
		},
	}

	tests := []struct {
		name            string
		reviewer        string
		allowedBranches []string
		want            []PRSummary
	}{
		{
			name:            "returns only open PRs assigned to reviewer on allowed branches",
			reviewer:        "alice",
			allowedBranches: []string{"trunk"},
			want:            []PRSummary{prs[0]},
		},
		{
			name:            "returns none when reviewer is not assigned",
			reviewer:        "carol",
			allowedBranches: []string{"trunk"},
			want:            nil,
		},
		{
			name:            "returns none when branch is not allowed",
			reviewer:        "alice",
			allowedBranches: []string{"users/alice/feature"},
			want:            nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterEligiblePRs(prs, tt.reviewer, tt.allowedBranches)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FilterEligiblePRs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFilterEligiblePRsUsesNormalizedTargetBranch(t *testing.T) {
	prs, err := ParsePRListJSON([]byte(`[
  {
    "id": "source-matches-target-does-not",
    "reviewers": ["alice"],
    "source_branch": "users/alice/review",
    "target_branch": "release",
    "status": "open"
  },
  {
    "id": "target-matches",
    "reviewers": ["alice"],
    "source_branch": "users/alice/other",
    "target_branch": "trunk",
    "status": "open"
  }
]`))
	if err != nil {
		t.Fatalf("ParsePRListJSON() error = %v", err)
	}

	got := FilterEligiblePRs(prs, "alice", []string{"trunk"})
	want := []PRSummary{prs[1]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FilterEligiblePRs() = %#v, want %#v", got, want)
	}
}
