package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

type arcPRReviewCycleConfig struct {
	PRID       string
	Workspace  string
	RepoRoot   string
	Model      string
	Timeout    time.Duration
	MaxRetries int
	Metadata   map[string]string
	AllowShip  bool

	StateFetcher  arcPRReviewCycleStateFetcher
	RevisionStore arcPRReviewCycleRevisionStore
	ModelHelper   arcPRReviewCycleModelHelper
	ReviewApplier arcPRReviewCycleReviewApplier
	ReplyApplier  arcPRReviewCycleReplyApplier
	ShipGate      arcPRReviewCycleShipGate
}

type arcPRReviewCycleStateFetcher interface {
	FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error)
}

type arcPRReviewCycleRevisionStore interface {
	GetReviewedRevision(ctx context.Context, prID string) (string, error)
}

type arcPRReviewCycleModelHelper interface {
	RunArcPRReviewModel(ctx context.Context, input arcPRReviewModelInput) ([]byte, error)
}

type arcPRReviewCycleReviewApplier interface {
	Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReviewResult, error)
}

type arcPRReviewCycleReplyApplier interface {
	Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReplyResult, error)
}

type arcPRReviewCycleShipGate interface {
	GateAndShip(ctx context.Context, request arcreview.ShipGateRequest) (arcreview.ShipGateResult, error)
}

type arcPRReviewCycleModelHelperFunc func(context.Context, arcPRReviewModelInput) ([]byte, error)

func (f arcPRReviewCycleModelHelperFunc) RunArcPRReviewModel(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
	return f(ctx, input)
}

func runArcPRReviewCycle(ctx context.Context, cfg arcPRReviewCycleConfig) (arcreview.PRRunnerAction, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.StateFetcher == nil {
		return "", errors.New("arc PR review state fetcher is required")
	}
	if cfg.RevisionStore == nil {
		return "", errors.New("arc PR review revision store is required")
	}

	state, err := cfg.StateFetcher.FetchPRRuntimeState(ctx, cfg.Workspace, cfg.PRID)
	if err != nil {
		return "", fmt.Errorf("fetch PR runtime state: %w", err)
	}

	prID := arcPRReviewCyclePRID(state, cfg.PRID)
	handledRevision, err := cfg.RevisionStore.GetReviewedRevision(ctx, prID)
	if err != nil {
		return "", fmt.Errorf("get reviewed revision for PR %q: %w", prID, err)
	}

	action := arcreview.PlanNextPRRunnerAction(state, handledRevision, cfg.AllowShip)
	switch action {
	case arcreview.PRRunnerActionReview:
		if cfg.ModelHelper == nil {
			return action, errors.New("arc PR review model helper is required")
		}
		if cfg.ReviewApplier == nil {
			return action, errors.New("arc PR review applier is required")
		}
		payload, err := cfg.ModelHelper.RunArcPRReviewModel(ctx, arcPRReviewCycleModelInput(cfg, state))
		if err != nil {
			return action, fmt.Errorf("run arc PR review model: %w", err)
		}
		if _, err := cfg.ReviewApplier.Apply(ctx, state, payload); err != nil {
			return action, fmt.Errorf("apply arc PR review: %w", err)
		}
	case arcreview.PRRunnerActionAnswer:
		if cfg.ModelHelper == nil {
			return action, errors.New("arc PR review model helper is required")
		}
		if cfg.ReplyApplier == nil {
			return action, errors.New("arc PR reply applier is required")
		}
		payload, err := cfg.ModelHelper.RunArcPRReviewModel(ctx, arcPRReviewCycleModelInput(cfg, state))
		if err != nil {
			return action, fmt.Errorf("run arc PR reply model: %w", err)
		}
		if _, err := cfg.ReplyApplier.Apply(ctx, state, payload); err != nil {
			return action, fmt.Errorf("apply arc PR reply: %w", err)
		}
	case arcreview.PRRunnerActionShip:
		if cfg.ShipGate == nil {
			return action, errors.New("arc PR ship gate is required")
		}
		if _, err := cfg.ShipGate.GateAndShip(ctx, arcreview.ShipGateRequest{
			State:            state,
			ReviewedRevision: handledRevision,
			AllowShip:        cfg.AllowShip,
		}); err != nil {
			return action, fmt.Errorf("gate and ship arc PR: %w", err)
		}
	}

	return action, nil
}

func arcPRReviewCycleModelInput(cfg arcPRReviewCycleConfig, state arcreview.PRRuntimeState) arcPRReviewModelInput {
	return arcPRReviewModelInput{
		State:      state,
		Model:      cfg.Model,
		RepoRoot:   cfg.RepoRoot,
		Timeout:    cfg.Timeout,
		MaxRetries: cfg.MaxRetries,
		Metadata:   cloneArcPRReviewModelMetadata(cfg.Metadata),
	}
}

func arcPRReviewCyclePRID(state arcreview.PRRuntimeState, fallback string) string {
	if prID := strings.TrimSpace(state.PRID); prID != "" {
		return prID
	}
	if prID := strings.TrimSpace(state.Details.ID); prID != "" {
		return prID
	}
	return strings.TrimSpace(fallback)
}
