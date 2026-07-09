package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const (
	runnerPRReviewDefaultStartrekEndpoint = "https://api.tracker.yandex.net/v3"
	runnerPRReviewStartrekTokenEnv        = "STARTREK_TOKEN"
)

type runnerPRReviewRuntime struct {
	StateFetcher        arcPRReviewCycleStateFetcher
	ModelHelper         arcPRReviewCycleModelHelper
	LinkedTicketTracker arcreview.LinkedTicketTracker
	Model               string
	RepoRoot            string
	Timeout             time.Duration
	MaxRetries          int
	Metadata            map[string]string
}

type runnerPRReviewRuntimeResolver func(context.Context, workitem.Item, envpreset.Workspace, workitem.PRReviewPayload) (runnerPRReviewRuntime, error)

func newRunnerPRReviewKindHandler(resolve runnerPRReviewRuntimeResolver) runnerKindHandler {
	if resolve == nil {
		resolve = defaultRunnerPRReviewRuntimeResolver
	}
	return func(ctx context.Context, item workitem.Item, workspace envpreset.Workspace) (workqueue.Result, error) {
		return runRunnerPRReview(ctx, item, workspace, resolve)
	}
}

func runRunnerPRReview(ctx context.Context, item workitem.Item, workspace envpreset.Workspace, resolve runnerPRReviewRuntimeResolver) (result workqueue.Result, err error) {
	if item.Kind != workitem.KindPRReview {
		return workqueue.Result{}, fmt.Errorf("PR review handler received kind %q", item.Kind)
	}
	if resolve == nil {
		resolve = defaultRunnerPRReviewRuntimeResolver
	}

	payload, err := workitem.DecodePRReviewPayload(item.Payload)
	if err != nil {
		return workqueue.Result{}, fmt.Errorf("decode PR review payload for item %q: %w", item.ID, err)
	}

	checkout, err := arcanum.PreparePRCheckout(payload.PRID)
	if err != nil {
		return workqueue.Result{}, fmt.Errorf("prepare PR checkout for item %q PR %q: %w", item.ID, strings.TrimSpace(payload.PRID), err)
	}
	if checkout == nil {
		return workqueue.Result{}, fmt.Errorf("prepare PR checkout for item %q PR %q returned empty mount path", item.ID, strings.TrimSpace(payload.PRID))
	}
	defer func() {
		if checkout.Cleanup == nil {
			return
		}
		if cleanupErr := checkout.Cleanup(); cleanupErr != nil {
			if err != nil {
				err = errors.Join(err, cleanupErr)
				return
			}
			err = cleanupErr
		}
	}()
	prMountPath := strings.TrimSpace(checkout.MountPath)
	if prMountPath == "" {
		return workqueue.Result{}, fmt.Errorf("prepare PR checkout for item %q PR %q returned empty mount path", item.ID, strings.TrimSpace(payload.PRID))
	}

	reviewWorkspace := workspace
	reviewWorkspace.Path = prMountPath

	runtime, err := resolve(ctx, item, reviewWorkspace, payload)
	if err != nil {
		return workqueue.Result{}, err
	}
	repoRoot := prMountPath

	projectContextFetcher := arcreview.ProjectContextFetcher{
		LinkedTicketTracker: runtime.LinkedTicketTracker,
	}

	mode := strings.TrimSpace(payload.Mode)
	capture := &runnerPRReviewResultCapture{}
	_, err = runArcPRReviewCycle(ctx, arcPRReviewCycleConfig{
		PRID:                  strings.TrimSpace(payload.PRID),
		Workspace:             runnerPRReviewWorkspacePath(reviewWorkspace),
		RepoRoot:              repoRoot,
		Model:                 strings.TrimSpace(runtime.Model),
		Timeout:               runtime.Timeout,
		MaxRetries:            runtime.MaxRetries,
		Metadata:              runnerPRReviewMetadata(item, runtime.Metadata),
		AllowShip:             payload.Ship,
		Mode:                  mode,
		StateFetcher:          runtime.StateFetcher,
		ProjectContextFetcher: projectContextFetcher,
		RevisionStore:         runnerPRReviewPayloadRevisionStore{payload: payload},
		ModelHelper:           runtime.ModelHelper,
		ReviewApplier: runnerPRReviewCapturingReviewApplier{
			inner: arcreview.ReviewApplier{
				Client: runnerPRReviewNoopReviewClient{},
				Store:  runnerPRReviewNoopReviewedRevisionStore{},
			},
			capture: capture,
		},
		ReplyApplier: runnerPRReviewCapturingReplyApplier{
			inner: arcreview.ReplyApplier{
				Client: runnerPRReviewNoopReplyClient{},
				Store:  runnerPRReviewNoopAnsweredCommentStore{},
			},
			capture: capture,
			mode:    mode,
		},
		ShipGate: runnerPRReviewCapturingShipGate{capture: capture},
	})
	if err != nil {
		return workqueue.Result{}, err
	}

	resultPayload, err := json.Marshal(capture.result)
	if err != nil {
		return workqueue.Result{}, fmt.Errorf("marshal PR review result for item %q: %w", item.ID, err)
	}
	return workqueue.Result{Payload: resultPayload}, nil
}

func defaultRunnerPRReviewRuntimeResolver(_ context.Context, item workitem.Item, workspace envpreset.Workspace, _ workitem.PRReviewPayload) (runnerPRReviewRuntime, error) {
	presetName := strings.TrimSpace(item.Preset)
	presets, err := envpreset.Load(defaultRunnerEnvironmentsPath())
	if err != nil {
		return runnerPRReviewRuntime{}, err
	}
	preset, ok := presets[presetName]
	if !ok {
		return runnerPRReviewRuntime{}, fmt.Errorf("preset %q not found in %s", presetName, defaultRunnerEnvironmentsPath())
	}
	resolvedAgent, err := envpreset.ResolveAgent(preset)
	if err != nil {
		return runnerPRReviewRuntime{}, err
	}

	repoRoot := strings.TrimSpace(workspace.Path)
	if repoRoot == "" {
		repoRoot = runnerPRReviewPresetRepoRoot(preset)
	}
	catalog, err := loadCodingAgentsCatalog(repoRoot)
	if err != nil {
		return runnerPRReviewRuntime{}, err
	}
	runner, err := buildAgentRunner(catalog, resolvedAgent.Backend, resolvedAgent.Model, resolvedAgent.RunnerTimeout)
	if err != nil {
		return runnerPRReviewRuntime{}, err
	}

	return runnerPRReviewRuntime{
		StateFetcher: arcPRReviewCycleStateFetcherFunc(arcanum.FetchPRRuntimeState),
		ModelHelper: arcPRReviewCycleModelHelperFunc(func(ctx context.Context, input arcPRReviewModelInput) ([]byte, error) {
			return runArcPRReviewModel(ctx, runner, input)
		}),
		LinkedTicketTracker: defaultRunnerPRReviewLinkedTicketTracker(),
		Model:               resolvedAgent.Model,
		RepoRoot:            repoRoot,
		Timeout:             resolvedAgent.RunnerTimeout,
		Metadata: map[string]string{
			"backend": strings.TrimSpace(resolvedAgent.Backend),
		},
	}, nil
}

func defaultRunnerPRReviewLinkedTicketTracker() arcreview.LinkedTicketTracker {
	token := strings.TrimSpace(os.Getenv(runnerPRReviewStartrekTokenEnv))
	if token == "" {
		return nil
	}
	client, err := startrek.NewClient(startrek.Config{
		Endpoint: runnerPRReviewDefaultStartrekEndpoint,
		Token:    token,
	})
	if err != nil {
		return nil
	}
	return client
}

func runnerPRReviewWorkspacePath(workspace envpreset.Workspace) string {
	if path := strings.TrimSpace(workspace.Path); path != "" {
		return path
	}
	if workspace.Strategy == envpreset.WorkspaceStrategyArcShared {
		return filepath.Join(strings.TrimSpace(workspace.Mount), strings.TrimSpace(workspace.Subpath))
	}
	return strings.TrimSpace(workspace.Origin)
}

func runnerPRReviewPresetRepoRoot(preset envpreset.Preset) string {
	if path := strings.TrimSpace(preset.Workspace.Path); path != "" {
		return path
	}
	if preset.Workspace.Strategy == envpreset.WorkspaceStrategyArcShared {
		return filepath.Join(strings.TrimSpace(preset.Workspace.Mount), strings.TrimSpace(preset.Workspace.Subpath))
	}
	return strings.TrimSpace(preset.Workspace.Origin)
}

func runnerPRReviewMetadata(item workitem.Item, base map[string]string) map[string]string {
	metadata := cloneArcPRReviewModelMetadata(base)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["phase"] = "pr_review"
	metadata["item_id"] = strings.TrimSpace(item.ID)
	metadata["preset"] = strings.TrimSpace(item.Preset)
	metadata["source"] = strings.TrimSpace(item.Source)
	metadata["source_ref"] = strings.TrimSpace(item.SourceRef)
	return metadata
}

type runnerPRReviewPayloadRevisionStore struct {
	payload workitem.PRReviewPayload
}

func (s runnerPRReviewPayloadRevisionStore) GetReviewedRevision(context.Context, string) (string, error) {
	if s.payload.Ship || len(s.payload.UnansweredCommentIDs) > 0 {
		return strings.TrimSpace(s.payload.Revision), nil
	}
	return "", nil
}

type runnerPRReviewResultCapture struct {
	result workitem.PRReviewResult
}

func (c *runnerPRReviewResultCapture) captureReview(state arcreview.PRRuntimeState, result arcreview.ReviewResult) {
	c.result.Summary = strings.TrimSpace(result.Summary)
	c.result.InlineComments = runnerPRReviewInlineComments(result.InlineComments)
	c.result.Replies = runnerPRReviewReplies(result.Replies)
	c.result.ReviewVerdict = strings.TrimSpace(result.Ship.Verdict)
	c.result.ShipReason = strings.TrimSpace(result.Ship.Reason)
	c.result.ShipReady = runnerPRReviewReviewShipReady(result)
	c.result.RevisionReviewed = runnerPRReviewCurrentRevision(state)
}

func (c *runnerPRReviewResultCapture) captureReply(result arcreview.ReplyResult, mode string, payload []byte) {
	if runnerPRReviewIsAuthorMode(mode) {
		c.result.CommentDecisions = runnerPRReviewCommentDecisions(payload)
		return
	}
	c.result.Replies = runnerPRReviewReplies(result.Replies)
}

// runnerPRReviewIsAuthorMode reports whether a pr-review payload selects author
// mode, where the agent triages review comments on its own PR. An empty or
// "reviewer" mode is the default reviewer mode.
func runnerPRReviewIsAuthorMode(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), workitem.PRReviewModeAuthor)
}

// runnerPRReviewCommentDecisions maps the author-mode model output into the
// queue result schema, mirroring runnerPRReviewReplies for the reviewer path.
func runnerPRReviewCommentDecisions(payload []byte) []workitem.PRReviewCommentDecision {
	decisions, err := arcreview.ParseAuthorDecisionResult(payload)
	if err != nil || len(decisions.Decisions) == 0 {
		return nil
	}
	out := make([]workitem.PRReviewCommentDecision, 0, len(decisions.Decisions))
	for _, decision := range decisions.Decisions {
		out = append(out, workitem.PRReviewCommentDecision{
			CommentID: strings.TrimSpace(decision.CommentID),
			Decision:  strings.TrimSpace(decision.Decision),
			Language:  strings.TrimSpace(decision.Language),
			ReplyBody: strings.TrimSpace(decision.ReplyBody),
			Rationale: strings.TrimSpace(decision.Rationale),
			Scope:     runnerPRReviewImplementScope(decision.Scope),
		})
	}
	return out
}

func runnerPRReviewImplementScope(scope *arcreview.AuthorImplementScope) *workitem.PRReviewImplementScope {
	if scope == nil {
		return nil
	}
	return &workitem.PRReviewImplementScope{
		Title:        strings.TrimSpace(scope.Title),
		Instructions: strings.TrimSpace(scope.Instructions),
		TargetFiles:  scope.TargetFiles,
	}
}

func (c *runnerPRReviewResultCapture) captureShip(state arcreview.PRRuntimeState) {
	c.result.ReviewVerdict = "ship"
	c.result.ShipReady = true
	c.result.RevisionReviewed = runnerPRReviewCurrentRevision(state)
}

type runnerPRReviewCapturingReviewApplier struct {
	inner   arcreview.ReviewApplier
	capture *runnerPRReviewResultCapture
}

func (a runnerPRReviewCapturingReviewApplier) Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReviewResult, error) {
	result, err := a.inner.Apply(ctx, state, payload)
	if err != nil {
		return arcreview.ReviewResult{}, err
	}
	if a.capture != nil {
		a.capture.captureReview(state, result)
	}
	return result, nil
}

type runnerPRReviewCapturingReplyApplier struct {
	inner   arcreview.ReplyApplier
	capture *runnerPRReviewResultCapture
	mode    string
}

func (a runnerPRReviewCapturingReplyApplier) Apply(ctx context.Context, state arcreview.PRRuntimeState, payload []byte) (arcreview.ReplyResult, error) {
	result, err := a.inner.Apply(ctx, state, payload)
	if err != nil {
		return arcreview.ReplyResult{}, err
	}
	if a.capture != nil {
		a.capture.captureReply(result, a.mode, payload)
	}
	return result, nil
}

type runnerPRReviewCapturingShipGate struct {
	capture *runnerPRReviewResultCapture
}

func (g runnerPRReviewCapturingShipGate) GateAndShip(_ context.Context, request arcreview.ShipGateRequest) (arcreview.ShipGateResult, error) {
	if g.capture != nil {
		g.capture.captureShip(request.State)
	}
	return arcreview.ShipGateResult{}, nil
}

type runnerPRReviewNoopReviewClient struct{}

func (runnerPRReviewNoopReviewClient) PostReviewInlineComment(context.Context, string, string, arcreview.ReviewInlineComment) error {
	return nil
}

func (runnerPRReviewNoopReviewClient) PostReviewSummary(context.Context, string, string, string) error {
	return nil
}

type runnerPRReviewNoopReviewedRevisionStore struct{}

func (runnerPRReviewNoopReviewedRevisionStore) StoreReviewedRevision(context.Context, string, string) error {
	return nil
}

type runnerPRReviewNoopReplyClient struct{}

func (runnerPRReviewNoopReplyClient) PostCommentReply(context.Context, string, string, string) error {
	return nil
}

type runnerPRReviewNoopAnsweredCommentStore struct{}

func (runnerPRReviewNoopAnsweredCommentStore) ListAnsweredCommentIDs(context.Context, string) ([]string, error) {
	return nil, nil
}

func (runnerPRReviewNoopAnsweredCommentStore) StoreAnsweredCommentIDs(context.Context, string, []string) error {
	return nil
}

func runnerPRReviewReplies(replies []arcreview.ReviewReply) []workitem.PRReviewReply {
	if replies == nil {
		return nil
	}
	out := make([]workitem.PRReviewReply, 0, len(replies))
	for _, reply := range replies {
		out = append(out, workitem.PRReviewReply{
			CommentID: strings.TrimSpace(reply.CommentID),
			Body:      strings.TrimSpace(reply.Body),
		})
	}
	return out
}

func runnerPRReviewInlineComments(comments []arcreview.ReviewInlineComment) []workitem.PRReviewInlineComment {
	if comments == nil {
		return nil
	}
	out := make([]workitem.PRReviewInlineComment, 0, len(comments))
	for _, comment := range comments {
		out = append(out, workitem.PRReviewInlineComment{
			Path:     strings.TrimSpace(comment.Path),
			Line:     comment.Line,
			Body:     strings.TrimSpace(comment.Body),
			Severity: strings.TrimSpace(comment.Severity),
		})
	}
	return out
}

func runnerPRReviewReviewShipReady(result arcreview.ReviewResult) bool {
	return strings.EqualFold(strings.TrimSpace(result.Ship.Verdict), "ship") && len(result.Blockers) == 0
}

func runnerPRReviewCurrentRevision(state arcreview.PRRuntimeState) string {
	if revision := strings.TrimSpace(state.Revision); revision != "" {
		return revision
	}
	return strings.TrimSpace(state.Details.Revision)
}
