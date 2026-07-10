package arcreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type PRReviewCycleConfig struct {
	PRID       string
	Workspace  string
	RepoRoot   string
	Model      string
	Timeout    time.Duration
	MaxRetries int
	Metadata   map[string]string
	AllowShip  bool

	// Mode selects the pr-review mode (see the PRReviewMode* constants in the
	// workitem package): the default reviewer mode, or author mode, where the
	// agent triages review comments on its own PR. It is threaded into the model
	// input so the model helper can build the matching prompt.
	Mode string

	StateFetcher          PRReviewCycleStateFetcher
	ProjectContextFetcher PRReviewCycleProjectContextFetcher
	RevisionStore         PRReviewCycleRevisionStore
	ModelHelper           PRReviewCycleModelHelper
	ReviewApplier         PRReviewCycleReviewApplier
	ReplyApplier          PRReviewCycleReplyApplier
	ResolveApplier        PRReviewCycleResolveApplier
	ShipGate              PRReviewCycleShipGate
}

type PRReviewCycleStateFetcher interface {
	FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (PRRuntimeState, error)
}

type PRReviewCycleRevisionStore interface {
	GetReviewedRevision(ctx context.Context, prID string) (string, error)
}

type PRReviewCycleProjectContextFetcher interface {
	FetchProjectContext(ctx context.Context, workspace string, state PRRuntimeState) (ProjectContext, error)
}

type PRReviewModelInput struct {
	State          PRRuntimeState
	ProjectContext ProjectContext
	Mode           string
	Model          string
	RepoRoot       string
	Timeout        time.Duration
	MaxRetries     int
	Metadata       map[string]string
	OnProgress     func(contracts.RunnerProgress)
}

type PRReviewCycleModelHelper interface {
	RunArcPRReviewModel(ctx context.Context, input PRReviewModelInput) ([]byte, error)
}

type PRReviewCycleReviewApplier interface {
	Apply(ctx context.Context, state PRRuntimeState, payload []byte) (ReviewResult, error)
}

type PRReviewCycleReplyApplier interface {
	Apply(ctx context.Context, state PRRuntimeState, payload []byte) (ReplyResult, error)
}

type PRReviewCycleResolveApplier interface {
	Apply(ctx context.Context, state PRRuntimeState, payload []byte) (ResolveResult, error)
}

type PRReviewCycleShipGate interface {
	GateAndShip(ctx context.Context, request ShipGateRequest) (ShipGateResult, error)
}

type PRReviewCycleModelHelperFunc func(context.Context, PRReviewModelInput) ([]byte, error)

func (f PRReviewCycleModelHelperFunc) RunArcPRReviewModel(ctx context.Context, input PRReviewModelInput) ([]byte, error) {
	return f(ctx, input)
}

type PRReviewCycleProjectContextFetcherFunc func(context.Context, string, PRRuntimeState) (ProjectContext, error)

func (f PRReviewCycleProjectContextFetcherFunc) FetchProjectContext(ctx context.Context, workspace string, state PRRuntimeState) (ProjectContext, error) {
	return f(ctx, workspace, state)
}

func RunPRReviewCycle(ctx context.Context, cfg PRReviewCycleConfig) (PRRunnerAction, error) {
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

	prID := prReviewCyclePRID(state, cfg.PRID)
	handledRevision, err := cfg.RevisionStore.GetReviewedRevision(ctx, prID)
	if err != nil {
		return "", fmt.Errorf("get reviewed revision for PR %q: %w", prID, err)
	}

	action := planPRReviewCycleAction(state, handledRevision, cfg.AllowShip, cfg.Mode)
	switch action {
	case PRRunnerActionReview:
		if cfg.ModelHelper == nil {
			return action, errors.New("arc PR review model helper is required")
		}
		if cfg.ReviewApplier == nil {
			return action, errors.New("arc PR review applier is required")
		}
		projectContext, err := prReviewCycleProjectContext(ctx, cfg, state)
		if err != nil {
			return action, err
		}
		payload, err := cfg.ModelHelper.RunArcPRReviewModel(ctx, prReviewCycleModelInput(cfg, state, projectContext))
		if err != nil {
			return action, fmt.Errorf("run arc PR review model: %w", err)
		}
		if _, err := cfg.ReviewApplier.Apply(ctx, state, payload); err != nil {
			return action, fmt.Errorf("apply arc PR review: %w", err)
		}
	case PRRunnerActionAnswer:
		if cfg.ModelHelper == nil {
			return action, errors.New("arc PR review model helper is required")
		}
		if cfg.ReplyApplier == nil {
			return action, errors.New("arc PR reply applier is required")
		}
		projectContext, err := prReviewCycleProjectContext(ctx, cfg, state)
		if err != nil {
			return action, err
		}
		payload, err := cfg.ModelHelper.RunArcPRReviewModel(ctx, prReviewCycleModelInput(cfg, state, projectContext))
		if err != nil {
			return action, fmt.Errorf("run arc PR reply model: %w", err)
		}
		if _, err := cfg.ReplyApplier.Apply(ctx, state, payload); err != nil {
			return action, fmt.Errorf("apply arc PR reply: %w", err)
		}
	case PRRunnerActionShip:
		if cfg.ShipGate == nil {
			return action, errors.New("arc PR ship gate is required")
		}
		if _, err := cfg.ShipGate.GateAndShip(ctx, ShipGateRequest{
			State:            state,
			ReviewedRevision: handledRevision,
			AllowShip:        cfg.AllowShip,
		}); err != nil {
			return action, fmt.Errorf("gate and ship arc PR: %w", err)
		}
	}

	return action, nil
}

func planPRReviewCycleAction(state PRRuntimeState, handledRevision string, allowShip bool, mode string) PRRunnerAction {
	if !strings.EqualFold(strings.TrimSpace(mode), "author") {
		return PlanNextPRRunnerAction(state, handledRevision, allowShip)
	}

	// Author mode produces per-comment decisions, not reviewer findings. Its
	// action therefore depends on comment state rather than revision identity;
	// Arcanum discovery uses active_diff_set IDs while runtime state exposes
	// from_id commit hashes, so comparing them would always select review.
	if isTerminalPRStatus(state.Details.Status) {
		return PRRunnerActionWait
	}
	if hasUnansweredPRComments(state.Comments) {
		return PRRunnerActionAnswer
	}
	if hasOpenPRBlockers(state.OpenIssues) || hasFailedPRChecks(state.Checks) || hasPendingPRChecks(state.Checks) || hasUnknownPRChecks(state.Checks) {
		return PRRunnerActionWait
	}
	if allowShip {
		return PRRunnerActionShip
	}
	return PRRunnerActionWait
}

func prReviewCycleProjectContext(ctx context.Context, cfg PRReviewCycleConfig, state PRRuntimeState) (ProjectContext, error) {
	if cfg.ProjectContextFetcher == nil {
		return ProjectContext{}, nil
	}
	projectContext, err := cfg.ProjectContextFetcher.FetchProjectContext(ctx, cfg.Workspace, state)
	if err != nil {
		return ProjectContext{}, fmt.Errorf("build arc PR review project context: %w", err)
	}
	return projectContext, nil
}

func prReviewCycleModelInput(cfg PRReviewCycleConfig, state PRRuntimeState, projectContext ProjectContext) PRReviewModelInput {
	return PRReviewModelInput{
		State:          state,
		ProjectContext: projectContext,
		Mode:           cfg.Mode,
		Model:          cfg.Model,
		RepoRoot:       cfg.RepoRoot,
		Timeout:        cfg.Timeout,
		MaxRetries:     cfg.MaxRetries,
		Metadata:       clonePRReviewModelMetadata(cfg.Metadata),
	}
}

func prReviewCyclePRID(state PRRuntimeState, fallback string) string {
	if prID := strings.TrimSpace(state.PRID); prID != "" {
		return prID
	}
	if prID := strings.TrimSpace(state.Details.ID); prID != "" {
		return prID
	}
	return strings.TrimSpace(fallback)
}

func clonePRReviewModelMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
