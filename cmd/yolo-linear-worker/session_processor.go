package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/claude"
	"github.com/anomalyco/yolo-runner/internal/codex"
	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/kimi"
	"github.com/anomalyco/yolo-runner/internal/linear"
	"github.com/anomalyco/yolo-runner/internal/linear/webhook"
	"github.com/anomalyco/yolo-runner/internal/opencode"
)

const (
	envLinearWorkerBackend       = "YOLO_LINEAR_WORKER_BACKEND"
	envLinearWorkerBinary        = "YOLO_LINEAR_WORKER_BINARY"
	envLinearWorkerModel         = "YOLO_LINEAR_WORKER_MODEL"
	envLinearWorkerRepoRoot      = "YOLO_LINEAR_WORKER_REPO_ROOT"
	envLinearWorkerRunnerTimeout = "YOLO_LINEAR_WORKER_RUNNER_TIMEOUT"
	envLinearAPIEndpoint         = "LINEAR_API_ENDPOINT"
	envLinearToken               = "LINEAR_TOKEN"
	envLinearAPIToken            = "LINEAR_API_TOKEN"

	defaultLinearGraphQLEndpoint = "https://api.linear.app/graphql"
	defaultLinearWorkerBackend   = "opencode"

	runnerSessionExternalURLLabel = "Runner Session"
	runnerLogExternalURLLabel     = "Runner Log"
)

type linearSessionActivityEmitter interface {
	EmitThought(context.Context, linear.ThoughtActivityInput) (string, error)
	EmitResponse(context.Context, linear.ResponseActivityInput) (string, error)
}

type linearSessionUpdater interface {
	SetExternalURLs(context.Context, linear.UpdateAgentSessionExternalURLsInput) error
}

type linearSessionJobProcessor struct {
	repoRoot      string
	backend       string
	model         string
	runnerTimeout time.Duration
	runner        contracts.AgentRunner
	activities    linearSessionActivityEmitter
	sessions      linearSessionUpdater
}

func defaultProcessLinearSessionJob(ctx context.Context, job webhook.Job) error {
	processor, err := newLinearSessionJobProcessorFromEnv()
	if err != nil {
		return err
	}
	return processor.Process(ctx, job)
}

func newLinearSessionJobProcessorFromEnv() (*linearSessionJobProcessor, error) {
	repoRoot := strings.TrimSpace(os.Getenv(envLinearWorkerRepoRoot))
	if repoRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve linear worker repo root: %w", err)
		}
		repoRoot = wd
	}
	repoRoot = ensureLinearWorkerRepoPath(repoRoot)

	backend := strings.ToLower(strings.TrimSpace(firstNonEmptyEnv(envLinearWorkerBackend, "YOLO_AGENT_BACKEND")))
	if backend == "" {
		backend = defaultLinearWorkerBackend
	}
	binary := strings.TrimSpace(os.Getenv(envLinearWorkerBinary))
	runner, err := newLinearWorkerRunner(backend, binary)
	if err != nil {
		return nil, err
	}

	runnerTimeout, err := resolveLinearWorkerRunnerTimeout()
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimSpace(os.Getenv(envLinearAPIEndpoint))
	if endpoint == "" {
		endpoint = defaultLinearGraphQLEndpoint
	}
	token := strings.TrimSpace(firstNonEmptyEnv(envLinearToken, envLinearAPIToken))
	if token == "" {
		return nil, fmt.Errorf("%s is required", envLinearToken)
	}

	activityClient, err := linear.NewAgentActivityClient(linear.AgentActivityClientConfig{
		Endpoint: endpoint,
		Token:    token,
	})
	if err != nil {
		return nil, fmt.Errorf("build linear activity client: %w", err)
	}
	sessionClient, err := linear.NewAgentSessionClient(linear.AgentSessionClientConfig{
		Endpoint: endpoint,
		Token:    token,
	})
	if err != nil {
		return nil, fmt.Errorf("build linear session client: %w", err)
	}

	return &linearSessionJobProcessor{
		repoRoot:      repoRoot,
		backend:       backend,
		model:         strings.TrimSpace(os.Getenv(envLinearWorkerModel)),
		runnerTimeout: runnerTimeout,
		runner:        runner,
		activities:    activityClient,
		sessions:      sessionClient,
	}, nil
}

func resolveLinearWorkerRunnerTimeout() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(envLinearWorkerRunnerTimeout))
	if raw == "" {
		return 0, nil
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", envLinearWorkerRunnerTimeout, err)
	}
	if timeout < 0 {
		return 0, fmt.Errorf("%s must be greater than or equal to 0", envLinearWorkerRunnerTimeout)
	}
	return timeout, nil
}

func newLinearWorkerRunner(backend string, binary string) (contracts.AgentRunner, error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "opencode":
		return opencode.NewCLIRunnerAdapter(opencode.CommandRunner{}, nil, "", ""), nil
	case "codex":
		return codex.NewCLIRunnerAdapter(binary, nil), nil
	case "claude":
		return claude.NewCLIRunnerAdapter(binary, nil), nil
	case "kimi":
		return kimi.NewCLIRunnerAdapter(binary, nil), nil
	default:
		return nil, fmt.Errorf("unsupported linear worker backend %q", backend)
	}
}

func (p *linearSessionJobProcessor) Process(ctx context.Context, job webhook.Job) error {
	if p == nil {
		return fmt.Errorf("linear session job processor is nil")
	}
	if p.runner == nil {
		return fmt.Errorf("linear session runner is nil")
	}
	if p.activities == nil {
		return fmt.Errorf("linear session activity emitter is nil")
	}
	if p.sessions == nil {
		return fmt.Errorf("linear session updater is nil")
	}

	sessionID := resolveLinearSessionID(job)
	baseKey := resolveLinearIdempotencyBase(job, sessionID)
	if err := p.updateSessionExternalURLs(ctx, job, sessionID); err != nil {
		return fmt.Errorf("update linear session external urls: %w", err)
	}

	if _, err := p.activities.EmitThought(ctx, linear.ThoughtActivityInput{
		AgentSessionID: sessionID,
		Body:           thoughtBodyForLinearJob(job),
		IdempotencyKey: baseKey + ":thought",
	}); err != nil {
		return fmt.Errorf("emit linear thought activity: %w", err)
	}

	result, runErr := p.runner.Run(ctx, contracts.RunnerRequest{
		TaskID:     normalizeLinearJobTaskID(job),
		Prompt:     buildLinearJobPrompt(job),
		Mode:       contracts.RunnerModeImplement,
		Model:      p.model,
		RepoRoot:   p.repoRoot,
		Timeout:    p.runnerTimeout,
		Metadata:   buildLinearRunnerMetadata(job, sessionID),
		OnProgress: nil,
	})

	responseBody := responseBodyForLinearJob(job, result, runErr)
	if _, err := p.activities.EmitResponse(ctx, linear.ResponseActivityInput{
		AgentSessionID: sessionID,
		Body:           responseBody,
		IdempotencyKey: baseKey + ":response",
	}); err != nil {
		if runErr != nil {
			return fmt.Errorf("run linear session job: %v; emit linear response activity: %w", runErr, err)
		}
		return fmt.Errorf("emit linear response activity: %w", err)
	}

	if runErr != nil {
		return fmt.Errorf("run linear session job: %w", runErr)
	}
	return nil
}

func resolveLinearSessionID(job webhook.Job) string {
	sessionID := strings.TrimSpace(job.SessionID)
	if sessionID != "" {
		return sessionID
	}
	sessionID = strings.TrimSpace(job.Event.AgentSession.ID)
	if sessionID != "" {
		return sessionID
	}
	return "unknown-session"
}

func resolveLinearIdempotencyBase(job webhook.Job, sessionID string) string {
	base := strings.TrimSpace(job.IdempotencyKey)
	if base != "" {
		return base
	}

	base = strings.TrimSpace(job.ID)
	if base != "" {
		return base
	}

	stepID := strings.TrimSpace(job.StepID)
	action := linearJobAction(job)
	if stepID != "" && action != "" {
		return sessionID + ":" + action + ":" + stepID
	}

	if action != "" {
		return sessionID + ":" + action
	}

	return sessionID + ":queued"
}

func normalizeLinearJobTaskID(job webhook.Job) string {
	raw := strings.TrimSpace(job.ID)
	if raw == "" {
		raw = strings.TrimSpace(job.IdempotencyKey)
	}
	if raw == "" {
		raw = "linear-session-job"
	}
	return sanitizeLinearJobID(raw)
}

func sanitizeLinearJobID(raw string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		" ", "_",
	)
	sanitized := strings.TrimSpace(replacer.Replace(raw))
	if sanitized == "" {
		return "linear-session-job"
	}
	return sanitized
}

func (p *linearSessionJobProcessor) updateSessionExternalURLs(ctx context.Context, job webhook.Job, sessionID string) error {
	if p == nil {
		return fmt.Errorf("linear session job processor is nil")
	}
	if p.sessions == nil {
		return fmt.Errorf("linear session updater is nil")
	}

	urls := mergeLinearExternalURLs(
		job.Event.AgentSession.ExternalURLs,
		runnerExternalURLsForLinearJob(job, p.repoRoot, p.backend),
	)
	if len(urls) == 0 {
		return nil
	}

	return p.sessions.SetExternalURLs(ctx, linear.UpdateAgentSessionExternalURLsInput{
		AgentSessionID: sessionID,
		ExternalURLs:   urls,
	})
}

func runnerExternalURLsForLinearJob(job webhook.Job, repoRoot string, backend string) []linear.AgentExternalURL {
	backend = strings.TrimSpace(backend)
	if backend == "" {
		backend = defaultLinearWorkerBackend
	}

	sessionPath := filepath.Join("runner-logs", backend)
	if strings.TrimSpace(repoRoot) != "" {
		sessionPath = filepath.Join(repoRoot, sessionPath)
	}

	logPath := filepath.Join(sessionPath, normalizeLinearJobTaskID(job)+".jsonl")
	return []linear.AgentExternalURL{
		{
			Label: runnerSessionExternalURLLabel,
			URL:   fileURLForPath(sessionPath),
		},
		{
			Label: runnerLogExternalURLLabel,
			URL:   fileURLForPath(logPath),
		},
	}
}

func mergeLinearExternalURLs(existing []linear.AgentExternalURL, additions []linear.AgentExternalURL) []linear.AgentExternalURL {
	merged := make([]linear.AgentExternalURL, 0, len(existing)+len(additions))
	seen := map[string]struct{}{}
	appendUnique := func(entry linear.AgentExternalURL) {
		label := strings.TrimSpace(entry.Label)
		value := strings.TrimSpace(entry.URL)
		if label == "" || value == "" {
			return
		}
		key := strings.ToLower(label) + "\n" + value
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		merged = append(merged, linear.AgentExternalURL{
			Label: label,
			URL:   value,
		})
	}

	for _, entry := range existing {
		appendUnique(entry)
	}
	for _, entry := range additions {
		appendUnique(entry)
	}
	return merged
}

func fileURLForPath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: filepath.Clean(cleaned)}).String()
}

func buildLinearRunnerMetadata(job webhook.Job, sessionID string) map[string]string {
	metadata := map[string]string{
		"linear_session_id": sessionID,
	}
	if id := strings.TrimSpace(job.ID); id != "" {
		metadata["linear_job_id"] = id
	}
	if action := linearJobAction(job); action != "" {
		metadata["linear_step_action"] = action
	}
	if stepID := strings.TrimSpace(job.StepID); stepID != "" {
		metadata["linear_step_id"] = stepID
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func linearJobAction(job webhook.Job) string {
	if action := strings.TrimSpace(string(job.StepAction)); action != "" {
		return action
	}
	if action := strings.TrimSpace(string(job.Event.Action)); action != "" {
		return action
	}
	return ""
}

func thoughtBodyForLinearJob(job webhook.Job) string {
	switch linear.AgentSessionEventAction(linearJobAction(job)) {
	case linear.AgentSessionEventActionCreated:
		return "Processing queued Linear session request."
	case linear.AgentSessionEventActionPrompted:
		return "Processing queued follow-up prompt for Linear session."
	default:
		return "Processing queued Linear session step."
	}
}

func responseBodyForLinearJob(job webhook.Job, result contracts.RunnerResult, runErr error) string {
	action := linearJobAction(job)
	if action == "" {
		action = "queued"
	}
	if runErr != nil {
		return fmt.Sprintf("Failed processing Linear session %s step: %s", action, strings.TrimSpace(runErr.Error()))
	}

	message := fmt.Sprintf("Finished processing Linear session %s step.", action)
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		message += " " + reason
	}
	return message
}

func buildLinearJobPrompt(job webhook.Job) string {
	action := linear.AgentSessionEventAction(linearJobAction(job))

	basePrompt := strings.TrimSpace(job.Event.AgentSession.PromptContext)
	if action == linear.AgentSessionEventActionPrompted {
		if reconstructed := strings.TrimSpace(linear.ReconstructPromptContext(job.Event, nil)); reconstructed != "" {
			basePrompt = reconstructed
		}
	}

	parts := make([]string, 0, 3)
	if basePrompt != "" {
		parts = append(parts, basePrompt)
	}
	if action == linear.AgentSessionEventActionCreated {
		if job.Event.AgentSession.Comment != nil {
			if body := strings.TrimSpace(job.Event.AgentSession.Comment.Body); body != "" {
				parts = append(parts, "Initial request:\n"+body)
			}
		}
	}
	if action == linear.AgentSessionEventActionPrompted {
		if job.Event.AgentActivity != nil {
			if body := strings.TrimSpace(job.Event.AgentActivity.Content.Body); body != "" {
				parts = append(parts, "Follow-up input:\n"+body)
			}
		}
		parts = append(parts, "Continue handling the Linear AgentSession request.")
	}

	if len(parts) == 0 {
		if rawPayload := strings.TrimSpace(string(job.Payload)); rawPayload != "" {
			parts = append(parts, rawPayload)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "Continue handling the Linear AgentSession request.")
	}
	return strings.Join(parts, "\n\n")
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func ensureLinearWorkerRepoPath(repoRoot string) string {
	if strings.TrimSpace(repoRoot) == "" {
		return ""
	}
	return filepath.Clean(repoRoot)
}
