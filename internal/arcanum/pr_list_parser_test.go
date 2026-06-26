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

func TestParsePRListJSONAcceptsEmptyDataObjectAsEmptyList(t *testing.T) {
	got, err := ParsePRListJSON([]byte(`{"data":{}}`))
	if err != nil {
		t.Fatalf("ParsePRListJSON(empty data object) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParsePRListJSON(empty data object) = %#v, want empty", got)
	}
}

func TestParsePRListJSONAcceptsNestedDataItemsWrapper(t *testing.T) {
	got, err := ParsePRListJSON([]byte(`{
  "data": {
    "items": [
      {"id":"42","summary":"nested PR","author":{"login":"alice"},"reviewers":["bob"],"from_id":"rev-42","status":"open"}
    ]
  }
}`))
	if err != nil {
		t.Fatalf("ParsePRListJSON(nested data items wrapper) error = %v", err)
	}

	want := []PRSummary{
		{
			ID:        "42",
			Title:     "nested PR",
			Summary:   "nested PR",
			Author:    "alice",
			Reviewers: []string{"bob"},
			FromID:    "rev-42",
			Status:    "open",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRListJSON(nested data items wrapper) = %#v, want %#v", got, want)
	}
}

// TestParsePRListJSONArcanumHTTPShape locks in the contract returned by the
// Arcanum /v1/review-requests collection (query DSL + fields projection): the
// items live under data.review_requests, branches nest under vcs, and the only
// per-push identifier is active_diff_set.id (used as the revision change-token).
// Regression for the discovery bug where the collection returned {data:{}}.
func TestParsePRListJSONArcanumHTTPShape(t *testing.T) {
	fixture := []byte(`{
  "data": {
    "review_requests": [
      {
        "id": 14107203,
        "author": {"name": "genaevstratov"},
        "summary": "Add Dino Messenger Deploy env helper",
        "vcs": {"from_branch": "users/genaevstratov/dino-messenger-deploy-env", "to_branch": "trunk"},
        "status": "published",
        "reviewers": []
      }
    ]
  }
}`)

	got, err := ParsePRListJSON(fixture)
	if err != nil {
		t.Fatalf("ParsePRListJSON(arcanum http shape) error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ParsePRListJSON(arcanum http shape) len = %d, want 1", len(got))
	}
	pr := got[0]
	if pr.ID != "14107203" {
		t.Errorf("ID = %q, want 14107203", pr.ID)
	}
	if pr.Author != "genaevstratov" {
		t.Errorf("Author = %q, want genaevstratov", pr.Author)
	}
	if pr.Summary != "Add Dino Messenger Deploy env helper" {
		t.Errorf("Summary = %q", pr.Summary)
	}
	if pr.Status != "published" {
		t.Errorf("Status = %q, want published", pr.Status)
	}
	if pr.FromBranch != "users/genaevstratov/dino-messenger-deploy-env" {
		t.Errorf("FromBranch = %q", pr.FromBranch)
	}
	if pr.ToBranch != "trunk" {
		t.Errorf("ToBranch = %q, want trunk", pr.ToBranch)
	}
}