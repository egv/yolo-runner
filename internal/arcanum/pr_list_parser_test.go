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

func TestParsePRListJSONAcceptsSingleObject(t *testing.T) {
	got, err := ParsePRListJSON([]byte(`{"id":"42","summary":"one PR","author":"alice","reviewers":["bob"],"from_id":"rev-42","status":"open"}`))
	if err != nil {
		t.Fatalf("ParsePRListJSON(single object) error = %v", err)
	}

	want := []PRSummary{
		{
			ID:        "42",
			Title:     "one PR",
			Summary:   "one PR",
			Author:    "alice",
			Reviewers: []string{"bob"},
			FromID:    "rev-42",
			Status:    "open",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRListJSON(single object) = %#v, want %#v", got, want)
	}
}

func TestParsePRListJSONAcceptsDataWrapper(t *testing.T) {
	got, err := ParsePRListJSON([]byte(`{
  "data": [
    {"id":"42","summary":"wrapped PR","author":{"login":"alice"},"reviewers":["bob"],"from_id":"rev-42","status":"open"}
  ]
}`))
	if err != nil {
		t.Fatalf("ParsePRListJSON(data wrapper) error = %v", err)
	}

	want := []PRSummary{
		{
			ID:        "42",
			Title:     "wrapped PR",
			Summary:   "wrapped PR",
			Author:    "alice",
			Reviewers: []string{"bob"},
			FromID:    "rev-42",
			Status:    "open",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRListJSON(data wrapper) = %#v, want %#v", got, want)
	}
}
