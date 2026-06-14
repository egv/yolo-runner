package arcanum

import (
	"reflect"
	"testing"
)

func TestParsePRListJSONNormalizesSummaries(t *testing.T) {
	fixture := []byte(`{
  "pull_requests": [
    {
      "id": 123456,
      "title": "ARW-01 Add review watcher",
      "author": {"login": "alice"},
      "reviewers": [
        {"login": "bob"},
        {"login": "carol"}
      ],
      "branch": "users/alice/arw-01",
      "status": "open"
    },
    {
      "id": "ARCADIA-789",
      "summary": "ARW-02 Handle comments",
      "created_by": "dave",
      "reviewers": ["erin"],
      "source_branch": "users/dave/arw-02",
      "target_branch": "trunk",
      "state": "merged"
    }
  ]
}`)

	got, err := ParsePRListJSON(fixture)
	if err != nil {
		t.Fatalf("ParsePRListJSON() error = %v", err)
	}

	want := []PRSummary{
		{
			ID:        "123456",
			Title:     "ARW-01 Add review watcher",
			Summary:   "ARW-01 Add review watcher",
			Author:    "alice",
			Reviewers: []string{"bob", "carol"},
			Branch:    "users/alice/arw-01",
			Status:    "open",
		},
		{
			ID:         "ARCADIA-789",
			Title:      "ARW-02 Handle comments",
			Summary:    "ARW-02 Handle comments",
			Author:     "dave",
			Reviewers:  []string{"erin"},
			Branch:     "trunk",
			FromBranch: "users/dave/arw-02",
			ToBranch:   "trunk",
			Status:     "merged",
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected PR summaries:\n got %#v\nwant %#v", got, want)
	}
}
