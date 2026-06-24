package workitem

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestKindValidation(t *testing.T) {
	validKinds := []Kind{
		KindImplement,
		KindReview,
		KindPreflight,
		KindSplit,
		KindPRReview,
		KindResolvePRComment,
		KindFinalize,
	}

	wantValues := map[Kind]string{
		KindImplement:         "implement",
		KindReview:            "review",
		KindPreflight:         "preflight",
		KindSplit:             "split",
		KindPRReview:          "pr-review",
		KindResolvePRComment:  "resolve-pr-comment",
		KindFinalize:          "finalize",
	}

	for _, kind := range validKinds {
		if !kind.IsValid() {
			t.Fatalf("%q should be valid", kind)
		}
		if err := kind.Validate(); err != nil {
			t.Fatalf("%q validation failed: %v", kind, err)
		}
		if string(kind) != wantValues[kind] {
			t.Fatalf("kind string = %q, want %q", kind, wantValues[kind])
		}
	}

	for _, kind := range []Kind{"", "unknown", "pr_review"} {
		if kind.IsValid() {
			t.Fatalf("%q should be invalid", kind)
		}
		if err := kind.Validate(); !errors.Is(err, ErrInvalidKind) {
			t.Fatalf("Validate(%q) error = %v, want ErrInvalidKind", kind, err)
		}
	}
}

func TestItemAndSubmissionRoundTripFields(t *testing.T) {
	payload := json.RawMessage(`{"task_id":"ADAPTABOT-12","labels":["queue"]}`)
	createdAt := time.Date(2026, 6, 12, 9, 30, 0, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Minute)
	notBefore := createdAt.Add(time.Hour)
	leaseExpiresAt := createdAt.Add(5 * time.Minute)
	heartbeatAt := createdAt.Add(time.Minute)

	item := Item{
		ID:             "01JY2ZE48V9H4H2AE4K2G0N1F8",
		Kind:           KindImplement,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-12",
		IdempotencyKey: "st/ADAPTABOT-12/implement/7",
		Preset:         "adapta",
		Priority:       50,
		Payload:        payload,
		State:          "claimed",
		Attempt:        2,
		MaxAttempts:    5,
		NotBefore:      notBefore,
		ClaimedBy:      "runner-1",
		LeaseExpiresAt: leaseExpiresAt,
		HeartbeatAt:    heartbeatAt,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}

	if item.Kind != KindImplement || !item.Kind.IsValid() {
		t.Fatalf("item kind did not round-trip: %q", item.Kind)
	}
	if item.ID != "01JY2ZE48V9H4H2AE4K2G0N1F8" ||
		item.Source != "st-adapta" ||
		item.SourceRef != "ADAPTABOT-12" ||
		item.IdempotencyKey != "st/ADAPTABOT-12/implement/7" ||
		item.Preset != "adapta" ||
		item.Priority != 50 ||
		item.State != "claimed" ||
		item.Attempt != 2 ||
		item.MaxAttempts != 5 ||
		item.ClaimedBy != "runner-1" {
		t.Fatalf("item scalar fields did not round-trip: %#v", item)
	}
	if !reflect.DeepEqual(item.Payload, payload) {
		t.Fatalf("item payload = %s, want %s", item.Payload, payload)
	}
	if !item.NotBefore.Equal(notBefore) ||
		!item.LeaseExpiresAt.Equal(leaseExpiresAt) ||
		!item.HeartbeatAt.Equal(heartbeatAt) ||
		!item.CreatedAt.Equal(createdAt) ||
		!item.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("item timestamps did not round-trip: %#v", item)
	}

	submission := Submission{
		Kind:           KindSplit,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-13",
		IdempotencyKey: "st/ADAPTABOT-13/split/3",
		Preset:         "adapta",
		Priority:       10,
		Payload:        json.RawMessage(`{"queue_root":"ADAPTABOT-1"}`),
		MaxAttempts:    4,
	}

	if submission.Kind != KindSplit || !submission.Kind.IsValid() {
		t.Fatalf("submission kind did not round-trip: %q", submission.Kind)
	}
	if submission.Source != "st-adapta" ||
		submission.SourceRef != "ADAPTABOT-13" ||
		submission.IdempotencyKey != "st/ADAPTABOT-13/split/3" ||
		submission.Preset != "adapta" ||
		submission.Priority != 10 ||
		submission.MaxAttempts != 4 {
		t.Fatalf("submission scalar fields did not round-trip: %#v", submission)
	}
	if !reflect.DeepEqual(submission.Payload, json.RawMessage(`{"queue_root":"ADAPTABOT-1"}`)) {
		t.Fatalf("submission payload = %s", submission.Payload)
	}
}
