package main

import (
	"context"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

type arcPRReviewCycleConfig = arcreview.PRReviewCycleConfig
type arcPRReviewCycleStateFetcher = arcreview.PRReviewCycleStateFetcher
type arcPRReviewCycleRevisionStore = arcreview.PRReviewCycleRevisionStore
type arcPRReviewCycleModelHelper = arcreview.PRReviewCycleModelHelper
type arcPRReviewCycleReviewApplier = arcreview.PRReviewCycleReviewApplier
type arcPRReviewCycleReplyApplier = arcreview.PRReviewCycleReplyApplier
type arcPRReviewCycleShipGate = arcreview.PRReviewCycleShipGate
type arcPRReviewModelInput = arcreview.PRReviewModelInput
type arcPRReviewCycleModelHelperFunc = arcreview.PRReviewCycleModelHelperFunc

func runArcPRReviewCycle(ctx context.Context, cfg arcPRReviewCycleConfig) (arcreview.PRRunnerAction, error) {
	return arcreview.RunPRReviewCycle(ctx, cfg)
}
