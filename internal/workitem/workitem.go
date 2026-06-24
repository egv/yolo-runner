package workitem

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Kind identifies the model-invoking job a runner should execute.
type Kind string

const (
	KindImplement        Kind = "implement"
	KindReview           Kind = "review"
	KindPreflight        Kind = "preflight"
	KindSplit            Kind = "split"
	KindPRReview         Kind = "pr-review"
	KindResolvePRComment Kind = "resolve-pr-comment"
	KindFinalize         Kind = "finalize"
)

// PR review modes discriminate pr-review work items. The default reviewer
// mode (the pre-existing behavior) is the empty string; "reviewer" is an
// accepted human-readable alias. Author mode is set explicitly by the source
// so the agent triages review comments on its own PR on the author's behalf.
const (
	PRReviewModeReviewer = ""
	PRReviewModeAuthor   = "author"
)

var ErrInvalidKind = errors.New("invalid work item kind")

func (k Kind) IsValid() bool {
	switch k {
	case KindImplement, KindReview, KindPreflight, KindSplit, KindPRReview, KindResolvePRComment, KindFinalize:
		return true
	default:
		return false
	}
}

func (k Kind) Validate() error {
	if k.IsValid() {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidKind, k)
}

// Item mirrors a row in the work_items queue table.
type Item struct {
	ID             string          `json:"id"`
	Kind           Kind            `json:"kind"`
	Source         string          `json:"source"`
	SourceRef      string          `json:"source_ref"`
	IdempotencyKey string          `json:"idempotency_key"`
	Preset         string          `json:"preset"`
	Priority       int             `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	Attempt        int             `json:"attempt"`
	MaxAttempts    int             `json:"max_attempts"`
	NotBefore      time.Time       `json:"not_before"`
	ClaimedBy      string          `json:"claimed_by"`
	LeaseExpiresAt time.Time       `json:"lease_expires_at"`
	HeartbeatAt    time.Time       `json:"heartbeat_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Submission is the source-facing request to enqueue a work item.
type Submission struct {
	Kind           Kind            `json:"kind"`
	Source         string          `json:"source"`
	SourceRef      string          `json:"source_ref"`
	IdempotencyKey string          `json:"idempotency_key"`
	Preset         string          `json:"preset"`
	Priority       int             `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	MaxAttempts    int             `json:"max_attempts"`
}
