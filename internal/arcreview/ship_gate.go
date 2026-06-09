package arcreview

import (
	"context"
	"fmt"
	"strings"
)

type ShipArcanumClient interface {
	Ship(ctx context.Context, prID string) error
}

type ShipGate struct {
	Client ShipArcanumClient
}

type ShipGateRequest struct {
	State             PRRuntimeState
	ReviewedRevision  string
	AllowShip         bool
	ModelShipDecision ReviewShipDecision
}

type ShipGateResult struct {
	Shipped bool
	Reasons []string
}

func (g ShipGate) GateAndShip(ctx context.Context, request ShipGateRequest) (ShipGateResult, error) {
	result := EvaluateShipGate(request)
	if len(result.Reasons) > 0 {
		return result, nil
	}
	if g.Client == nil {
		return result, fmt.Errorf("ship Arcanum client is required")
	}
	prID := currentReviewPRID(request.State)
	if err := g.Client.Ship(ctx, prID); err != nil {
		return result, fmt.Errorf("ship PR %q: %w", prID, err)
	}
	result.Shipped = true
	return result, nil
}

func EvaluateShipGate(request ShipGateRequest) ShipGateResult {
	var reasons []string

	if !request.AllowShip {
		reasons = append(reasons, "shipping is disabled")
	}
	if prID := currentReviewPRID(request.State); prID == "" {
		reasons = append(reasons, "PR ID is required")
	}

	currentRevision := currentPRRevision(request.State)
	reviewedRevision := strings.TrimSpace(request.ReviewedRevision)
	switch {
	case currentRevision == "":
		reasons = append(reasons, "current revision is unknown")
	case reviewedRevision != currentRevision:
		reasons = append(reasons, fmt.Sprintf("current revision %s is not reviewed", currentRevision))
	}

	if hasOpenPRBlockers(request.State.OpenIssues) {
		reasons = append(reasons, "open blockers remain")
	}
	if hasUnansweredPRComments(request.State.Comments) {
		reasons = append(reasons, "unanswered comments remain")
	}
	if hasFailedPRChecks(request.State.Checks) {
		reasons = append(reasons, "checks failed")
	}
	if hasPendingPRChecks(request.State.Checks) {
		reasons = append(reasons, "checks are pending")
	}
	if hasUnknownPRChecks(request.State.Checks) {
		reasons = append(reasons, "checks have unknown status")
	}
	if !modelApprovesShip(request.ModelShipDecision) {
		reasons = append(reasons, modelShipRejectionReason(request.ModelShipDecision))
	}

	return ShipGateResult{Reasons: reasons}
}

func modelApprovesShip(decision ReviewShipDecision) bool {
	return normalizedPRStatus(decision.Verdict) == "ship"
}

func modelShipRejectionReason(decision ReviewShipDecision) string {
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		return "model did not approve shipping: " + reason
	}
	verdict := strings.TrimSpace(decision.Verdict)
	if verdict == "" {
		return "model did not approve shipping"
	}
	return "model did not approve shipping: " + verdict
}
