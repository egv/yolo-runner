package arcanum

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// Arcanum creates the active diff set asynchronously after arc push.  In
	// practice the branch revision can take tens of seconds to become active,
	// so a handful of one-second checks is not a reliable publication check.
	publishVerificationAttempts = 10
	publishVerificationDelay    = 3 * time.Second
)

// PRPublishFunc performs one Arc publication attempt for an existing PR.
type PRPublishFunc func(context.Context, string) error

// PRPublicationVerifier reports whether Arcanum considers the active diff set
// for a PR published. It deliberately checks the server state instead of
// trusting a successful arc pr publish exit code.
type PRPublicationVerifier func(context.Context, string) error

// PublishAndVerifyPR retries publication until Arcanum reports that the active
// diff set is published. A task must not be considered landed while its latest
// version remains a draft.
func PublishAndVerifyPR(ctx context.Context, prID string, publish PRPublishFunc, verify PRPublicationVerifier) error {
	return publishAndVerifyPR(ctx, prID, publish, verify, publishVerificationAttempts, publishVerificationDelay)
}

func publishAndVerifyPR(ctx context.Context, prID string, publish PRPublishFunc, verify PRPublicationVerifier, attempts int, delay time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	if publish == nil {
		return fmt.Errorf("PR publisher is required")
	}
	if verify == nil {
		return fmt.Errorf("PR publication verifier is required")
	}
	if attempts < 1 {
		return fmt.Errorf("PR publication attempts must be positive")
	}
	if delay < 0 {
		return fmt.Errorf("PR publication retry delay cannot be negative")
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := publish(ctx, prID); err != nil {
			return fmt.Errorf("publish PR %q attempt %d: %w", prID, attempt, err)
		}
		if err := verify(ctx, prID); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == attempts {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("PR %q active diff set remained draft after %d publish attempts: %w", prID, attempts, lastErr)
}

// VerifyActiveDiffSetPublished checks Arcanum's active diff set for a PR using
// the same authenticated API client the Arc PR source already uses.
func VerifyActiveDiffSetPublished(ctx context.Context, prID string) error {
	return VerifyActiveDiffSetPublishedForRevision(ctx, prID, "")
}

// ActiveDiffSetMatchesRevision reports whether the active Arcanum diff set is
// still the revision represented by a queued review item. Unlike publication
// verification, draft status is not an error here: a current draft may still
// need the normal rebase-and-publish path. A false result means that a newer
// version has already superseded the queued item.
func ActiveDiffSetMatchesRevision(ctx context.Context, prID string, revision string) (bool, error) {
	client, err := NewAPIClient(APIClientConfig{BaseURL: DefaultAPIBaseURL})
	if err != nil {
		return false, err
	}
	return ActiveDiffSetMatchesRevisionWithClient(ctx, client, prID, revision)
}

// ActiveDiffSetMatchesRevisionWithClient is the testable variant of
// ActiveDiffSetMatchesRevision.
func ActiveDiffSetMatchesRevisionWithClient(ctx context.Context, client *APIClient, prID string, revision string) (bool, error) {
	if client == nil {
		return false, fmt.Errorf("Arcanum API client is required")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return false, fmt.Errorf("PR ID is required")
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return false, fmt.Errorf("revision is required")
	}
	var response reviewRequestResponse
	if err := client.GetJSON(ctx, reviewRequestDiffSetsPath(prID), &response); err != nil {
		return false, fmt.Errorf("fetch active diff set for PR %q: %w", prID, err)
	}
	return response.Data.ActiveDiffSet.matchesRevision(revision), nil
}

// VerifyActiveDiffSetPublishedForRevision checks that Arcanum has made the
// expected branch revision active and published. Matching the revision avoids
// accepting a previous published diff set while a newly pushed draft is still
// being materialized asynchronously.
func VerifyActiveDiffSetPublishedForRevision(ctx context.Context, prID string, revision string) error {
	client, err := NewAPIClient(APIClientConfig{BaseURL: DefaultAPIBaseURL})
	if err != nil {
		return err
	}
	return VerifyActiveDiffSetPublishedForRevisionWithClient(ctx, client, prID, revision)
}

// VerifyActiveDiffSetPublishedWithClient is the testable variant of
// VerifyActiveDiffSetPublished.
func VerifyActiveDiffSetPublishedWithClient(ctx context.Context, client *APIClient, prID string) error {
	return VerifyActiveDiffSetPublishedForRevisionWithClient(ctx, client, prID, "")
}

// VerifyActiveDiffSetPublishedForRevisionWithClient is the testable variant
// of VerifyActiveDiffSetPublishedForRevision.
func VerifyActiveDiffSetPublishedForRevisionWithClient(ctx context.Context, client *APIClient, prID string, revision string) error {
	if client == nil {
		return fmt.Errorf("Arcanum API client is required")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}

	var response reviewRequestResponse
	if err := client.GetJSON(ctx, reviewRequestDiffSetsPath(prID), &response); err != nil {
		return fmt.Errorf("fetch active diff set for PR %q: %w", prID, err)
	}
	active := response.Data.ActiveDiffSet
	if active.xid() == "" {
		return fmt.Errorf("PR %q has no active diff set", prID)
	}
	revision = strings.TrimSpace(revision)
	if revision != "" && !active.matchesRevision(revision) {
		return fmt.Errorf("PR %q active diff set %q does not match pushed revision %q", prID, active.xid(), revision)
	}
	if !strings.EqualFold(strings.TrimSpace(active.Status), "published") {
		return fmt.Errorf("PR %q active diff set %q status is %q", prID, active.xid(), active.Status)
	}
	return nil
}
