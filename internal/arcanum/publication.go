package arcanum

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	publishVerificationAttempts = 3
	publishVerificationDelay    = time.Second
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

	var lastErr error
	for attempt := 1; attempt <= publishVerificationAttempts; attempt++ {
		if err := publish(ctx, prID); err != nil {
			return fmt.Errorf("publish PR %q attempt %d: %w", prID, attempt, err)
		}
		if err := verify(ctx, prID); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if attempt == publishVerificationAttempts {
			break
		}
		timer := time.NewTimer(publishVerificationDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("PR %q active diff set remained draft after %d publish attempts: %w", prID, publishVerificationAttempts, lastErr)
}

// VerifyActiveDiffSetPublished checks Arcanum's active diff set for a PR using
// the same authenticated API client the Arc PR source already uses.
func VerifyActiveDiffSetPublished(ctx context.Context, prID string) error {
	client, err := NewAPIClient(APIClientConfig{BaseURL: DefaultAPIBaseURL})
	if err != nil {
		return err
	}
	return VerifyActiveDiffSetPublishedWithClient(ctx, client, prID)
}

// VerifyActiveDiffSetPublishedWithClient is the testable variant of
// VerifyActiveDiffSetPublished.
func VerifyActiveDiffSetPublishedWithClient(ctx context.Context, client *APIClient, prID string) error {
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
	if !strings.EqualFold(strings.TrimSpace(active.Status), "published") {
		return fmt.Errorf("PR %q active diff set %q status is %q", prID, active.xid(), active.Status)
	}
	return nil
}
