package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
)

type linearSessionActivityEmitter interface {
	EmitThought(context.Context, linear.ThoughtActivityInput) (string, error)
	EmitResponse(context.Context, linear.ResponseActivityInput) (string, error)
	UpdateSessionExternalURLs(context.Context, linear.AgentSessionExternalURLsInput) error
}

type linearIssueStatusStarter interface {
	EnsureIssueStarted(context.Context, string) error
}

type linearSessionJobProcessor struct {
	repoRoot      string
	model         string
	runnerTimeout time.Duration
	runner        contracts.AgentRunner
	activities    linearSessionActivityEmitter
	issueStarter  linearIssueStatusStarter
}

func defaultProcessLinearSessionJob(ctx context.Context, job webhook.Job) error {
	if err := validateQueuedLinearWebhookJob(job); err != nil {
		return err
	}

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

	return &linearSessionJobProcessor{
		repoRoot:      repoRoot,
		model:         strings.TrimSpace(os.Getenv(envLinearWorkerModel)),
		runnerTimeout: runnerTimeout,
		runner:        runner,
		activities:    activityClient,
		issueStarter:  &linearIssueStarterClient{endpoint: endpoint, token: token, httpClient: http.DefaultClient},
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
	if err := validateQueuedLinearWebhookJob(job); err != nil {
		return err
	}

	sessionID := resolveLinearSessionID(job)
	baseKey := resolveLinearIdempotencyBase(job, sessionID)

	if _, err := p.activities.EmitThought(ctx, linear.ThoughtActivityInput{
		AgentSessionID: sessionID,
		Body:           thoughtBodyForLinearJob(job),
		IdempotencyKey: baseKey + ":thought",
	}); err != nil {
		return fmt.Errorf("emit linear thought activity: %w", err)
	}
	if issueID := resolveDelegatedLinearIssueID(job); issueID != "" && p.issueStarter != nil {
		if err := p.issueStarter.EnsureIssueStarted(ctx, issueID); err != nil {
			return fmt.Errorf("transition delegated Linear issue %q to started: %w", issueID, err)
		}
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

	executionErr := linearSessionRunnerError(result, runErr)
	if externalURLs := linearSessionExternalURLsForRun(p.repoRoot, result); len(externalURLs) > 0 {
		if err := p.activities.UpdateSessionExternalURLs(ctx, linear.AgentSessionExternalURLsInput{
			AgentSessionID: sessionID,
			ExternalURLs:   externalURLs,
		}); err != nil {
			return fmt.Errorf("update linear session external urls: %w", err)
		}
	}

	responseBody := responseBodyForLinearJob(job, executionErr)
	if _, err := p.activities.EmitResponse(ctx, linear.ResponseActivityInput{
		AgentSessionID: sessionID,
		Body:           responseBody,
		IdempotencyKey: baseKey + ":response",
	}); err != nil {
		if executionErr != nil {
			return fmt.Errorf("run linear session job: %v; emit linear response activity: %w", executionErr, err)
		}
		return fmt.Errorf("emit linear response activity: %w", err)
	}

	if executionErr != nil {
		return fmt.Errorf("run linear session job: %w", executionErr)
	}
	return nil
}

func linearSessionRunnerError(result contracts.RunnerResult, runErr error) error {
	if runErr != nil {
		return runErr
	}
	if result.Status == "" || result.Status == contracts.RunnerResultCompleted {
		return nil
	}
	reason := strings.TrimSpace(result.Reason)
	if reason == "" {
		reason = fmt.Sprintf("runner returned %s status", result.Status)
	}
	return fmt.Errorf("%s", reason)
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

func resolveDelegatedLinearIssueID(job webhook.Job) string {
	if job.Event.AgentSession.Issue == nil {
		return ""
	}
	return strings.TrimSpace(job.Event.AgentSession.Issue.ID)
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

func responseBodyForLinearJob(job webhook.Job, runErr error) string {
	action := linearJobAction(job)
	if action == "" {
		action = "queued"
	}
	if runErr != nil {
		return fmt.Sprintf("Failed processing Linear session %s step.\n%s", action, FormatLinearSessionActionableError(runErr))
	}

	message := fmt.Sprintf("Finished processing Linear session %s step.", action)
	return message
}

func linearSessionExternalURLsForRun(repoRoot string, result contracts.RunnerResult) []linear.ExternalURL {
	urls := make([]linear.ExternalURL, 0, 3)

	if sessionURL := sessionExternalURL(result.Artifacts); sessionURL != "" {
		urls = append(urls, linear.ExternalURL{Label: "Runner session", URL: sessionURL})
	}

	if logURL := fileExternalURL(repoRoot, result.LogPath); logURL != "" {
		urls = append(urls, linear.ExternalURL{Label: "Runner log", URL: logURL})
	}
	if result.Artifacts != nil {
		if logURL := fileExternalURL(repoRoot, result.Artifacts["log_path"]); logURL != "" {
			urls = append(urls, linear.ExternalURL{Label: "Runner log artifact", URL: logURL})
		}
	}

	return urls
}

func sessionExternalURL(artifacts map[string]string) string {
	if len(artifacts) == 0 {
		return ""
	}
	if raw := strings.TrimSpace(artifacts["session_url"]); raw != "" {
		parsed, err := url.Parse(raw)
		if err == nil && strings.TrimSpace(parsed.Scheme) != "" {
			return raw
		}
	}
	if !strings.EqualFold(strings.TrimSpace(artifacts["backend"]), "opencode") {
		return ""
	}
	sessionID := strings.TrimSpace(artifacts["session_id"])
	if sessionID == "" {
		return ""
	}
	return "https://opencode.ai/sessions/" + url.PathEscape(sessionID)
}

func fileExternalURL(repoRoot string, pathValue string) string {
	resolved := strings.TrimSpace(pathValue)
	if resolved == "" {
		return ""
	}
	if !filepath.IsAbs(resolved) {
		if strings.TrimSpace(repoRoot) != "" {
			resolved = filepath.Join(repoRoot, resolved)
		}
	}
	if absolute, err := filepath.Abs(resolved); err == nil {
		resolved = absolute
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(resolved)}).String()
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

type linearIssueStarterClient struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

func (c *linearIssueStarterClient) EnsureIssueStarted(ctx context.Context, issueID string) error {
	if c == nil {
		return fmt.Errorf("linear issue starter client is nil")
	}

	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return nil
	}

	query := fmt.Sprintf(`query ReadIssueWorkflowForDelegatedRun {
  issue(id: %s) {
    id
    state {
      type
      name
    }
    team {
      states {
        nodes {
          id
          type
          name
        }
      }
    }
  }
}`, graphQLQuote(issueID))

	var payload struct {
		Issue *struct {
			ID    string `json:"id"`
			State *struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"state"`
			Team *struct {
				States struct {
					Nodes []struct {
						ID   string `json:"id"`
						Type string `json:"type"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	}
	if err := c.runGraphQLQuery(ctx, query, &payload); err != nil {
		return fmt.Errorf("query delegated issue workflow state: %w", err)
	}
	if payload.Issue == nil {
		return fmt.Errorf("delegated issue %q not found", issueID)
	}
	if payload.Issue.Team == nil {
		return fmt.Errorf("delegated issue %q has no team workflow states", issueID)
	}
	if isStartedOrTerminalState(payload.Issue.State) {
		return nil
	}

	startedStateID, ok := firstStartedWorkflowStateID(payload.Issue.Team.States.Nodes)
	if !ok {
		return fmt.Errorf("no started workflow state available for delegated issue %q", issueID)
	}

	mutation := fmt.Sprintf(`mutation UpdateIssueWorkflowStateForDelegatedRun {
  issueUpdate(id: %s, input: { stateId: %s }) {
    success
  }
}`, graphQLQuote(issueID), graphQLQuote(startedStateID))

	var mutationPayload struct {
		IssueUpdate *struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.runGraphQLQuery(ctx, mutation, &mutationPayload); err != nil {
		return fmt.Errorf("update delegated issue workflow state: %w", err)
	}
	if mutationPayload.IssueUpdate == nil || !mutationPayload.IssueUpdate.Success {
		return fmt.Errorf("update delegated issue workflow state unsuccessful")
	}
	return nil
}

func (c *linearIssueStarterClient) runGraphQLQuery(ctx context.Context, query string, out any) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("graphql query is required")
	}
	if strings.TrimSpace(c.endpoint) == "" {
		return fmt.Errorf("graphql endpoint is required")
	}
	if strings.TrimSpace(c.token) == "" {
		return fmt.Errorf("graphql token is required")
	}

	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return fmt.Errorf("marshal GraphQL request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build GraphQL request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send GraphQL request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read GraphQL response: %w", err)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("decode GraphQL response: %w", err)
		}
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("graphql errors: %s", joinLinearGraphQLErrors(envelope.Errors))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("graphql http %d: %s", resp.StatusCode, msg)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("graphql response missing data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode GraphQL data payload: %w", err)
	}
	return nil
}

func isStartedOrTerminalState(state *struct {
	Type string `json:"type"`
	Name string `json:"name"`
}) bool {
	if state == nil {
		return false
	}
	normalizedType := normalizeLinearStateToken(state.Type)
	switch normalizedType {
	case "started", "inprogress", "inprogressstate",
		"completed", "done", "closed", "canceled", "cancelled":
		return true
	}
	normalizedName := strings.ToLower(strings.TrimSpace(state.Name))
	return strings.Contains(normalizedName, "progress") ||
		strings.Contains(normalizedName, "doing") ||
		strings.Contains(normalizedName, "started") ||
		strings.Contains(normalizedName, "done") ||
		strings.Contains(normalizedName, "complete") ||
		strings.Contains(normalizedName, "cancel") ||
		strings.Contains(normalizedName, "close")
}

func firstStartedWorkflowStateID(states []struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}) (string, bool) {
	for _, state := range states {
		stateID := strings.TrimSpace(state.ID)
		if stateID == "" {
			continue
		}
		normalizedType := normalizeLinearStateToken(state.Type)
		if normalizedType == "started" || normalizedType == "inprogress" || normalizedType == "inprogressstate" {
			return stateID, true
		}
	}
	return "", false
}

func normalizeLinearStateToken(raw string) string {
	token := strings.ToLower(strings.TrimSpace(raw))
	token = strings.ReplaceAll(token, "_", "")
	token = strings.ReplaceAll(token, "-", "")
	token = strings.ReplaceAll(token, " ", "")
	return token
}

func joinLinearGraphQLErrors(errors []struct {
	Message string `json:"message"`
}) string {
	messages := make([]string, 0, len(errors))
	for _, entry := range errors {
		msg := strings.TrimSpace(entry.Message)
		if msg == "" {
			continue
		}
		messages = append(messages, msg)
	}
	if len(messages) == 0 {
		return "unknown graphql error"
	}
	return strings.Join(messages, "; ")
}

func graphQLQuote(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
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

func validateQueuedLinearWebhookJob(job webhook.Job) error {
	if job.ContractVersion != webhook.JobContractVersion1 {
		return fmt.Errorf("queued linear webhook job contract version must be %d", webhook.JobContractVersion1)
	}
	if strings.TrimSpace(job.IdempotencyKey) == "" {
		return fmt.Errorf("queued linear webhook job idempotency key is required")
	}
	if strings.TrimSpace(resolveLinearSessionID(job)) == "" {
		return fmt.Errorf("queued linear webhook job session id is required")
	}
	if strings.TrimSpace(job.StepID) == "" {
		return fmt.Errorf("queued linear webhook job step id is required")
	}

	switch linear.AgentSessionEventAction(linearJobAction(job)) {
	case linear.AgentSessionEventActionCreated, linear.AgentSessionEventActionPrompted:
		return nil
	default:
		return fmt.Errorf("queued linear webhook job step action must be created or prompted")
	}
}
