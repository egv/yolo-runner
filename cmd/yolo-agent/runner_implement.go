package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/executor"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

// runnerImplementPreparePRCheckout is a seam over arcanum.PreparePRCheckout so
// the author-mode (arcpr-author) branch can be tested without a real arc mount.
var runnerImplementPreparePRCheckout = func(prID string) (*arcanum.PRCheckout, error) {
	return arcanum.PreparePRCheckoutWithConfig(context.Background(), prID, arcanum.PRCheckoutConfig{Rebase: true})
}

// runnerImplementFetchPRComments is a seam over arcanum.FetchPRComments for the
// landing-applicability check.
var runnerImplementFetchPRComments = arcanum.FetchPRComments

// runnerImplementImplementSkippedStatus marks an implement result whose comment
// no longer needed a fix at landing time. The arcpr source treats it as
// terminal without posting a reply or resolving the thread.
const runnerImplementSkippedStatus = "skipped"

// runnerImplementCommentObsoleteReason re-checks an author-mode comment against
// the live PR right before landing. An implement item can sit in the queue long
// after triage (backlog, crash requeue, retries); by the time it runs, the
// comment may be resolved, deleted, or already answered by the PR author — in
// each case landing again would duplicate work and post a bogus reply. Returns
// a human-readable reason to skip, or "" when the fix is still applicable.
func runnerImplementCommentObsoleteReason(ctx context.Context, payload workitem.ImplementPayload) (string, error) {
	meta := payload.PromptContext.Metadata
	if strings.TrimSpace(meta["origin"]) != "arcpr-author" {
		return "", nil
	}
	prID := strings.TrimSpace(meta["arc_pr_id"])
	commentIDs := workitem.ImplementCommentIDs(meta)
	if prID == "" || len(commentIDs) == 0 {
		return "", nil
	}
	comments, err := runnerImplementFetchPRComments(ctx, prID)
	if err != nil {
		return "", fmt.Errorf("check arc PR comments %v applicability before landing: %w", commentIDs, err)
	}

	// The batch is skipped only when EVERY covered comment is obsolete; a
	// single still-live comment keeps the whole run worthwhile.
	var reasons []string
	for _, commentID := range commentIDs {
		reason := runnerImplementSingleCommentObsoleteReason(comments, prID, commentID, strings.TrimSpace(meta["arc_pr_author"]))
		if reason == "" {
			return "", nil
		}
		reasons = append(reasons, reason)
	}
	return strings.Join(reasons, "; "), nil
}

func runnerImplementSingleCommentObsoleteReason(comments []arcreview.PRComment, prID string, commentID string, author string) string {
	var root *arcreview.PRComment
	for i := range comments {
		if strings.TrimSpace(comments[i].ID) == commentID {
			root = &comments[i]
			break
		}
	}
	if root == nil {
		return fmt.Sprintf("comment %s no longer exists on PR %s", commentID, prID)
	}
	if root.Resolved {
		return fmt.Sprintf("comment %s on PR %s is already resolved", commentID, prID)
	}
	if author != "" {
		for _, comment := range comments {
			if strings.TrimSpace(comment.ThreadID) == commentID &&
				strings.EqualFold(strings.TrimSpace(comment.Author), author) {
				return fmt.Sprintf("comment %s on PR %s was already answered by %s", commentID, prID, author)
			}
		}
	}
	return ""
}

var runnerImplementPRVCS = func(path string) contracts.VCS {
	return arcvcs.New(localGitRunner{dir: path})
}

// runnerImplementPRLanding captures the per-PR checkout used when an implement
// item targets an existing Arc PR (metadata origin == "arcpr-author"). When
// disabled, the handler uses the normal materialized workspace + task branch.
type runnerImplementPRLanding struct {
	enabled          bool
	prID             string
	mountPath        string
	rebaseConflict   *arcanum.PRRebaseConflict
	cleanupFn        func() error
	cleanupContextFn func(context.Context) error
}

const runnerImplementPRCleanupTimeout = 30 * time.Second

func (l runnerImplementPRLanding) cleanup(ctx context.Context) {
	cleanupCtx, cancel := context.WithTimeout(ctx, runnerImplementPRCleanupTimeout)
	defer cancel()
	if l.cleanupContextFn != nil {
		_ = l.cleanupContextFn(cleanupCtx)
		return
	}
	if l.cleanupFn != nil {
		_ = l.cleanupFn()
	}
}

// resolveRunnerImplementPRLanding prepares a per-PR checkout for author-mode
// implement items so their fixes land on the existing PR branch (via
// push_existing_pr) instead of creating a new task branch off main.
func resolveRunnerImplementPRLanding(payload workitem.ImplementPayload, itemID string) (runnerImplementPRLanding, error) {
	none := runnerImplementPRLanding{}
	meta := payload.PromptContext.Metadata
	if strings.TrimSpace(meta["origin"]) != "arcpr-author" {
		return none, nil
	}
	prID := strings.TrimSpace(meta["arc_pr_id"])
	if prID == "" {
		return none, fmt.Errorf("arcpr-author implement item %q missing arc_pr_id metadata", itemID)
	}
	checkout, err := runnerImplementPreparePRCheckout(prID)
	if err != nil {
		return none, fmt.Errorf("prepare arc PR checkout for %q: %w", prID, err)
	}
	mountPath := strings.TrimSpace(checkout.MountPath)
	if mountPath == "" {
		if checkout.Cleanup != nil {
			_ = checkout.Cleanup()
		}
		return none, fmt.Errorf("arc PR checkout for %q returned empty mount path", prID)
	}
	return runnerImplementPRLanding{
		enabled:          true,
		prID:             prID,
		mountPath:        mountPath,
		rebaseConflict:   checkout.RebaseConflict,
		cleanupFn:        checkout.Cleanup,
		cleanupContextFn: checkout.CleanupContext,
	}, nil
}

// runnerImplementAuthorWorkspacePreamble is prepended to every author-mode
// implement prompt. The per-PR checkout is a disposable monorepo mount with a
// cold build cache: an agent that launches the service's full testsuite there
// burns the entire runner timeout on toolchain downloads and builds (observed:
// a 45m run spent 24m inside `ya make -t` and landed nothing).
const runnerImplementAuthorWorkspacePreamble = "Workspace policy: you are in a disposable per-PR checkout of the Arcadia monorepo with a COLD build cache. " +
	"Do NOT run the full service testsuite, `ya make -t` at service scope, or anything that builds the world — a cold-cache run exceeds your whole time budget, and CI validates the pushed revision anyway. " +
	"Validate with the smallest sufficient check only (compile the affected package, or a single targeted test with a tight filter), keeping total validation under ~5 minutes. " +
	"Your priority is to make the requested change and get it committed."

// runnerImplementRebaseConflictPreamble briefs the coding agent when the
// automatic trunk rebase stopped on merge conflicts: the agent must redo the
// rebase and resolve the conflicts before implementing the task, so the fix
// lands on a fresh base instead of failing terminally (the pre-agent rebase
// used to be a hard failure — docs/arcpr-system-status.md failure #2).
func runnerImplementRebaseConflictPreamble(conflict *arcanum.PRRebaseConflict) string {
	if conflict == nil {
		return ""
	}
	target := strings.TrimSpace(conflict.TargetBranch)
	if target == "" {
		target = "trunk"
	}
	var b strings.Builder
	b.WriteString("IMPORTANT — resolve the rebase first. This PR branch could not be rebased onto `")
	b.WriteString(target)
	b.WriteString("` automatically because of merge conflicts")
	if details := strings.TrimSpace(conflict.Details); details != "" {
		b.WriteString(":\n\n```\n")
		b.WriteString(details)
		b.WriteString("\n```")
	} else {
		b.WriteString(".")
	}
	b.WriteString("\n\nBefore the task below:\n")
	b.WriteString("1. Run `arc rebase ")
	b.WriteString(target)
	b.WriteString("`.\n")
	b.WriteString("2. For every conflicted file (`arc status` lists them), resolve the conflict preserving both this PR's intent and the current `")
	b.WriteString(target)
	b.WriteString("` behavior, then `arc add <file>`.\n")
	b.WriteString("3. Run `arc rebase --continue`; repeat resolve/add/continue until the rebase completes.\n")
	b.WriteString("Never use `arc rebase --abort`. Only after the rebase completes, implement the task below.")
	return b.String()
}

type runnerImplementExecutor struct {
	Runner  contracts.AgentRunner
	Agent   envpreset.ResolvedAgent
	Landing envpreset.LandingType
	Events  contracts.EventSink
}

type runnerImplementExecutorResolver func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error)

func newRunnerImplementKindHandler(resolve runnerImplementExecutorResolver) runnerKindHandler {
	if resolve == nil {
		resolve = defaultRunnerImplementExecutorResolver
	}
	return func(ctx context.Context, item workitem.Item, workspace envpreset.Workspace) (workqueue.Result, error) {
		payload, err := workitem.DecodeImplementPayload(item.Payload)
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("decode implement payload for item %q: %w", item.ID, err)
		}

		// Landing applicability gate: run before the checkout is prepared so an
		// obsolete comment costs one API call, not a mount + rebase + publish.
		obsoleteReason, err := runnerImplementCommentObsoleteReason(ctx, payload)
		if err != nil {
			return workqueue.Result{}, err
		}
		if obsoleteReason != "" {
			skipped, err := json.Marshal(workitem.ImplementResult{
				Status: runnerImplementSkippedStatus,
				Reason: obsoleteReason,
			})
			if err != nil {
				return workqueue.Result{}, fmt.Errorf("encode skipped implement result for item %q: %w", item.ID, err)
			}
			return workqueue.Result{Status: workqueue.ResultStatusCompleted, Payload: skipped}, nil
		}

		prLanding, err := resolveRunnerImplementPRLanding(payload, item.ID)
		if err != nil {
			return workqueue.Result{}, err
		}
		defer prLanding.cleanup(ctx)
		if !prLanding.enabled && workspace.VCS == nil {
			return workqueue.Result{}, fmt.Errorf("implement item %q requires an isolated VCS-bearing workspace; preset %q materialized none (path strategy is not allowed for code-writing kinds)", item.ID, item.Preset)
		}
		resolved, err := resolve(ctx, item, workspace)
		if err != nil {
			return workqueue.Result{}, err
		}
		if resolved.Runner == nil {
			return workqueue.Result{}, fmt.Errorf("implement runner for preset %q is nil", item.Preset)
		}

		repoRoot := strings.TrimSpace(workspace.Path)
		taskVCS := workspace.VCS
		landingMode := ""
		mergeOnSuccess := runnerImplementShouldLand(resolved.Landing)
		prIDForLanding := ""
		if prLanding.enabled {
			// Author-mode implement items land on the existing PR branch via a
			// per-PR checkout + push_existing_pr, not a task branch off main.
			repoRoot = prLanding.mountPath
			landingMode = executor.LandingModePushExistingPR
			mergeOnSuccess = true
			prIDForLanding = prLanding.prID
			taskVCS = runnerImplementPRVCS(prLanding.mountPath)
			if preamble := runnerImplementRebaseConflictPreamble(prLanding.rebaseConflict); preamble != "" {
				payload.PromptContext.Prompt = preamble + "\n\n" + payload.PromptContext.Prompt
			}
			payload.PromptContext.Prompt = strings.TrimSpace(runnerImplementAuthorWorkspacePreamble + "\n\n" + payload.PromptContext.Prompt)
		}
		if taskVCS == nil {
			return workqueue.Result{}, fmt.Errorf("implement item %q resolved no VCS adapter", item.ID)
		}

		exec := &executor.Executor{
			Runner:            resolved.Runner,
			Events:            resolved.Events,
			RepoRoot:          repoRoot,
			VCS:               taskVCS,
			ParentID:          strings.TrimSpace(payload.PromptContext.ParentID),
			Backend:           resolved.Agent.Backend,
			Model:             resolved.Agent.Model,
			RunnerTimeout:     resolved.Agent.RunnerTimeout,
			WatchdogTimeout:   resolved.Agent.WatchdogTimeout,
			WatchdogInterval:  resolved.Agent.WatchdogInterval,
			MaxRetries:        runnerImplementMaxRetries(item),
			RequireReview:     true,
			MergeOnSuccess:    mergeOnSuccess,
			LandingMode:       landingMode,
			PRIDForLanding:    prIDForLanding,
			WorkerID:          strings.TrimSpace(item.ClaimedBy),
			Priority:          item.Priority,
			QualityGateTools:  nil,
			QCGateTools:       nil,
			AllowLowQuality:   false,
			HeartbeatInterval: 0,
		}

		implementResult, err := exec.Execute(ctx, payload)
		if err != nil {
			return workqueue.Result{}, err
		}
		resultPayload, err := json.Marshal(implementResult)
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("encode implement result for item %q: %w", item.ID, err)
		}
		return workqueue.Result{
			Status:  runnerImplementResultStatus(implementResult.Status),
			Payload: resultPayload,
			LogPath: runnerImplementFirstNonEmpty(implementResult.Artifacts["log_path"], implementResult.Artifacts["review_log_path"]),
		}, nil
	}
}

func defaultRunnerImplementExecutorResolver(_ context.Context, item workitem.Item, _ envpreset.Workspace) (runnerImplementExecutor, error) {
	presets, err := envpreset.Load(defaultRunnerDaemonEnvironmentsPath)
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	return resolveRunnerImplementExecutorFromPresets(item, presets, defaultRunnerDaemonEnvironmentsPath)
}

func newRunnerImplementExecutorResolverForPresets(presets map[string]envpreset.Preset, events contracts.EventSink) runnerImplementExecutorResolver {
	return func(_ context.Context, item workitem.Item, _ envpreset.Workspace) (runnerImplementExecutor, error) {
		resolved, err := resolveRunnerImplementExecutorFromPresets(item, presets, "runner environments")
		if err != nil {
			return runnerImplementExecutor{}, err
		}
		resolved.Events = events
		return resolved, nil
	}
}

func resolveRunnerImplementExecutorFromPresets(item workitem.Item, presets map[string]envpreset.Preset, source string) (runnerImplementExecutor, error) {
	presetName := strings.TrimSpace(item.Preset)
	preset, ok := presets[presetName]
	if !ok {
		return runnerImplementExecutor{}, fmt.Errorf("preset %q not found in %s", presetName, source)
	}

	resolvedAgent, err := envpreset.ResolveAgent(preset)
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	landing, err := envpreset.ResolveLanding(preset)
	if err != nil {
		return runnerImplementExecutor{}, err
	}

	catalog, err := loadCodingAgentsCatalog("")
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	runner, err := buildAgentRunner(catalog, resolvedAgent.Backend, resolvedAgent.Model, resolvedAgent.RunnerTimeout)
	if err != nil {
		return runnerImplementExecutor{}, err
	}

	return runnerImplementExecutor{
		Runner:  runner,
		Agent:   resolvedAgent,
		Landing: landing,
	}, nil
}

func runnerImplementMaxRetries(item workitem.Item) int {
	if item.MaxAttempts <= 1 {
		return 0
	}
	return item.MaxAttempts - 1
}

func runnerImplementShouldLand(landing envpreset.LandingType) bool {
	switch landing {
	case envpreset.LandingTypeGitMerge, envpreset.LandingTypeArcPR:
		return true
	default:
		return false
	}
}

func runnerImplementResultStatus(status string) workqueue.ResultStatus {
	switch contracts.RunnerResultStatus(strings.TrimSpace(status)) {
	case contracts.RunnerResultBlocked:
		return workqueue.ResultStatusBlocked
	case contracts.RunnerResultFailed:
		return workqueue.ResultStatusFailed
	default:
		return workqueue.ResultStatusCompleted
	}
}

func runnerImplementFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
