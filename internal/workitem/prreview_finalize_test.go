package workitem

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPRReviewPayloadResultRoundTripAndDecode(t *testing.T) {
	payload := PRReviewPayload{
		PRID:                 "42",
		Revision:             "r7",
		UnansweredCommentIDs: []string{"comment-1", "comment-2"},
		Ship:                 true,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"pr_id": "42",
		"revision": "r7",
		"unanswered_comment_ids": ["comment-1", "comment-2"],
		"ship": true
	}`))

	withUnknown := []byte(`{
		"pr_id": "42",
		"revision": "r7",
		"unanswered_comment_ids": ["comment-1", "comment-2"],
		"ship": true,
		"future_payload_field": "ignored"
	}`)
	gotPayload, err := DecodePRReviewPayload(withUnknown)
	if err != nil {
		t.Fatalf("decode payload with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(gotPayload, payload) {
		t.Fatalf("decoded payload mismatch:\n got: %#v\nwant: %#v", gotPayload, payload)
	}

	emptyPayload := PRReviewPayload{PRID: "43", Revision: "r8"}
	raw, err = json.Marshal(emptyPayload)
	if err != nil {
		t.Fatalf("marshal empty payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"pr_id": "43",
		"revision": "r8",
		"ship": false
	}`))

	result := PRReviewResult{
		Replies: []PRReviewReply{
			{CommentID: "comment-1", Body: "Fixed by the latest revision."},
		},
		ReviewVerdict:    "ship",
		ShipReady:        true,
		RevisionReviewed: "r7",
	}

	raw, err = json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"replies": [
			{"comment_id": "comment-1", "body": "Fixed by the latest revision."}
		],
		"review_verdict": "ship",
		"ship_ready": true,
		"revision_reviewed": "r7"
	}`))

	withUnknown = []byte(`{
		"replies": [
			{
				"comment_id": "comment-1",
				"body": "Fixed by the latest revision.",
				"future_reply_field": "ignored"
			}
		],
		"review_verdict": "ship",
		"ship_ready": true,
		"revision_reviewed": "r7",
		"future_result_field": "ignored"
	}`)
	gotResult, err := DecodePRReviewResult(withUnknown)
	if err != nil {
		t.Fatalf("decode result with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(gotResult, result) {
		t.Fatalf("decoded result mismatch:\n got: %#v\nwant: %#v", gotResult, result)
	}
}

func TestFinalizePayloadResultRoundTripAndDecode(t *testing.T) {
	payload := FinalizePayload{
		ParentRef:     "ADAPTABOT-1",
		ChildBranches: []string{"task/ADAPTABOT-2", "task/ADAPTABOT-3"},
		Title:         "Land ADAPTABOT-1: Queue split",
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"parent_ref": "ADAPTABOT-1",
		"child_branches": ["task/ADAPTABOT-2", "task/ADAPTABOT-3"],
		"title": "Land ADAPTABOT-1: Queue split"
	}`))

	withUnknown := []byte(`{
		"parent_ref": "ADAPTABOT-1",
		"child_branches": ["task/ADAPTABOT-2", "task/ADAPTABOT-3"],
		"title": "Land ADAPTABOT-1: Queue split",
		"future_payload_field": "ignored"
	}`)
	gotPayload, err := DecodeFinalizePayload(withUnknown)
	if err != nil {
		t.Fatalf("decode payload with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(gotPayload, payload) {
		t.Fatalf("decoded payload mismatch:\n got: %#v\nwant: %#v", gotPayload, payload)
	}

	emptyPayload := FinalizePayload{
		ParentRef: "ADAPTABOT-4",
		Title:     "Land ADAPTABOT-4",
	}
	raw, err = json.Marshal(emptyPayload)
	if err != nil {
		t.Fatalf("marshal empty payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"parent_ref": "ADAPTABOT-4",
		"title": "Land ADAPTABOT-4"
	}`))

	result := FinalizeResult{PRURL: "https://arc.example.test/review/123"}
	raw, err = json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"pr_url": "https://arc.example.test/review/123"
	}`))

	withUnknown = []byte(`{
		"pr_url": "https://arc.example.test/review/123",
		"future_result_field": "ignored"
	}`)
	gotResult, err := DecodeFinalizeResult(withUnknown)
	if err != nil {
		t.Fatalf("decode result with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(gotResult, result) {
		t.Fatalf("decoded result mismatch:\n got: %#v\nwant: %#v", gotResult, result)
	}
}

func TestPRReviewPayloadModeRoundTrip(t *testing.T) {
	// Author mode is the new explicit discriminator and must round-trip.
	author := PRReviewPayload{
		PRID:     "42",
		Revision: "r7",
		Mode:     PRReviewModeAuthor,
		Ship:     true,
	}

	raw, err := json.Marshal(author)
	if err != nil {
		t.Fatalf("marshal author payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"pr_id": "42",
		"revision": "r7",
		"mode": "author",
		"ship": true
	}`))

	withUnknown := []byte(`{
		"pr_id": "42",
		"revision": "r7",
		"mode": "author",
		"ship": true,
		"future_payload_field": "ignored"
	}`)
	gotPayload, err := DecodePRReviewPayload(withUnknown)
	if err != nil {
		t.Fatalf("decode payload with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(gotPayload, author) {
		t.Fatalf("decoded payload mismatch:\n got: %#v\nwant: %#v", gotPayload, author)
	}

	// Reviewer is the default mode (PRReviewModeReviewer == ""), so an unset
	// Mode is omitted from JSON via omitempty.
	reviewer := PRReviewPayload{PRID: "43", Revision: "r8", Mode: PRReviewModeReviewer}
	raw, err = json.Marshal(reviewer)
	if err != nil {
		t.Fatalf("marshal reviewer payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"pr_id": "43",
		"revision": "r8",
		"ship": false
	}`))
}

func TestPRReviewResultCommentDecisionsRoundTrip(t *testing.T) {
	result := PRReviewResult{
		CommentDecisions: []PRReviewCommentDecision{
			{
				CommentID: "comment-1",
				Decision:  PRReviewCommentDecisionImplement,
				Language:  "ru",
				Rationale: "Needs a nil guard.",
				Scope: &PRReviewImplementScope{
					Title:        "Guard nil config in loader",
					Instructions: "Return early when config is nil.",
					TargetFiles:  []string{"internal/config/loader.go"},
				},
			},
			{
				CommentID: "comment-2",
				Decision:  PRReviewCommentDecisionResolve,
				Language:  "en",
				ReplyBody: "Good catch — already addressed in r7.",
			},
			{
				CommentID: "comment-3",
				Decision:  PRReviewCommentDecisionArgue,
				Language:  "en",
				ReplyBody: "This is intentional; the flag is documented.",
				Rationale: "Documented behavior, not a defect.",
			},
		},
		ReviewVerdict: "ship",
		ShipReady:     true,
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"comment_decisions": [
			{
				"comment_id": "comment-1",
				"decision": "implement",
				"language": "ru",
				"rationale": "Needs a nil guard.",
				"scope": {
					"title": "Guard nil config in loader",
					"instructions": "Return early when config is nil.",
					"target_files": ["internal/config/loader.go"]
				}
			},
			{
				"comment_id": "comment-2",
				"decision": "resolve",
				"language": "en",
				"reply_body": "Good catch — already addressed in r7."
			},
			{
				"comment_id": "comment-3",
				"decision": "argue",
				"language": "en",
				"reply_body": "This is intentional; the flag is documented.",
				"rationale": "Documented behavior, not a defect."
			}
		],
		"review_verdict": "ship",
		"ship_ready": true
	}`))

	// Unknown fields at every nesting level are tolerated.
	singleResult := PRReviewResult{
		CommentDecisions: []PRReviewCommentDecision{
			{
				CommentID: "comment-1",
				Decision:  PRReviewCommentDecisionImplement,
				Language:  "ru",
				Rationale: "Needs a nil guard.",
				Scope: &PRReviewImplementScope{
					Title:        "Guard nil config in loader",
					Instructions: "Return early when config is nil.",
					TargetFiles:  []string{"internal/config/loader.go"},
				},
			},
		},
		ReviewVerdict: "ship",
		ShipReady:     true,
	}
	withUnknown := []byte(`{
		"comment_decisions": [
			{
				"comment_id": "comment-1",
				"decision": "implement",
				"language": "ru",
				"rationale": "Needs a nil guard.",
				"scope": {
					"title": "Guard nil config in loader",
					"instructions": "Return early when config is nil.",
					"target_files": ["internal/config/loader.go"],
					"future_scope_field": "ignored"
				},
				"future_decision_field": "ignored"
			}
		],
		"review_verdict": "ship",
		"ship_ready": true,
		"future_result_field": "ignored"
	}`)
	gotResult, err := DecodePRReviewResult(withUnknown)
	if err != nil {
		t.Fatalf("decode result with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(gotResult, singleResult) {
		t.Fatalf("decoded result mismatch:\n got: %#v\nwant: %#v", gotResult, singleResult)
	}
}

func TestDecodeResolvePRCommentPayload(t *testing.T) {
	payload := ResolvePRCommentPayload{
		PRID:      "42",
		CommentID: "comment-7",
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	assertJSONEqual(t, raw, []byte(`{
		"pr_id": "42",
		"comment_id": "comment-7"
	}`))

	withUnknown := []byte(`{
		"pr_id": "42",
		"comment_id": "comment-7",
		"future_field": "ignored"
	}`)
	got, err := DecodeResolvePRCommentPayload(withUnknown)
	if err != nil {
		t.Fatalf("decode payload with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(got, payload) {
		t.Fatalf("decoded payload mismatch:\n got: %#v\nwant: %#v", got, payload)
	}
}
