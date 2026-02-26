package distributed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type TaskRewriteResult struct {
	RevisedTaskDescription    string   `json:"revised_task_description,omitempty"`
	RevisedAcceptanceCriteria []string `json:"revised_acceptance_criteria,omitempty"`
	Assumptions               []string `json:"assumptions,omitempty"`
	Rationale                 string   `json:"rationale,omitempty"`
	SelectedModel             string   `json:"selected_model,omitempty"`
	PolicyReason              string   `json:"policy_reason,omitempty"`
}

type TaskRewriteDecisionLog struct {
	RequestID     string
	CorrelationID string
	TaskID        string
	Service       string
	SelectedModel string
	PolicyReason  string
	Attempts      int
	Failure       string
}

type TaskRewriteModelPolicy struct {
	DefaultModel string
	LargerModel  string
}

func (p TaskRewriteModelPolicy) Select(request ServiceRequestPayload) (string, string) {
	metadata := request.Metadata
	if strings.EqualFold(strings.TrimSpace(metadata["rewrite_policy"]), "larger_model") {
		if model := strings.TrimSpace(p.LargerModel); model != "" {
			return model, "policy:larger_model"
		}
	}
	if model := strings.TrimSpace(metadata["model"]); model != "" {
		return model, "metadata:model"
	}
	if model := strings.TrimSpace(p.DefaultModel); model != "" {
		return model, "policy:default"
	}
	return "", "policy:none"
}

type MastermindTaskRewriteRequestHandlerOptions struct {
	RewriteRunner       contracts.AgentRunner
	DefaultRewriteModel string
	LargerRewriteModel  string
	MaxRetries          int
	AttemptTimeout      time.Duration
	RetryDelay          time.Duration
	DecisionLogger      func(context.Context, TaskRewriteDecisionLog)
}

type MastermindTaskRewriteRequestHandler struct {
	rewriteRunner  contracts.AgentRunner
	modelPolicy    TaskRewriteModelPolicy
	maxRetries     int
	attemptTimeout time.Duration
	retryDelay     time.Duration
	decisionLogger func(context.Context, TaskRewriteDecisionLog)
}

func NewMastermindTaskRewriteRequestHandler(opts MastermindTaskRewriteRequestHandlerOptions) *MastermindTaskRewriteRequestHandler {
	return &MastermindTaskRewriteRequestHandler{
		rewriteRunner: opts.RewriteRunner,
		modelPolicy: TaskRewriteModelPolicy{
			DefaultModel: strings.TrimSpace(opts.DefaultRewriteModel),
			LargerModel:  strings.TrimSpace(opts.LargerRewriteModel),
		},
		maxRetries:     max(0, opts.MaxRetries),
		attemptTimeout: opts.AttemptTimeout,
		retryDelay:     opts.RetryDelay,
		decisionLogger: opts.DecisionLogger,
	}
}

func (h *MastermindTaskRewriteRequestHandler) Handle(ctx context.Context, request ServiceRequestPayload) (ServiceResponsePayload, error) {
	if h == nil || h.rewriteRunner == nil {
		return ServiceResponsePayload{}, fmt.Errorf("task rewrite handler unavailable")
	}
	service := normalizeServiceName(request.Service)
	if !isTaskRewriteServiceName(service) {
		return ServiceResponsePayload{}, fmt.Errorf("unsupported task rewrite service %q", strings.TrimSpace(request.Service))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	selectedModel, policyReason := h.modelPolicy.Select(request)
	runnerRequest := buildTaskRewriteRunnerRequest(request, selectedModel)

	maxAttempts := h.maxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var finalResult contracts.RunnerResult
	var finalErr error
	attempts := 0
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		attemptCtx, cancel := contextForReviewAttempt(ctx, h.attemptTimeout)
		result, runErr := h.rewriteRunner.Run(attemptCtx, runnerRequest)
		cancel()
		finalResult = result
		finalErr = runErr
		if runErr == nil && result.Status == contracts.RunnerResultCompleted {
			break
		}
		if attempt == maxAttempts || ctx.Err() != nil {
			break
		}
		if h.retryDelay > 0 {
			select {
			case <-time.After(h.retryDelay):
			case <-ctx.Done():
				finalErr = ctx.Err()
				attempt = maxAttempts
			}
		}
	}

	response := ServiceResponsePayload{
		RequestID:     strings.TrimSpace(request.RequestID),
		CorrelationID: strings.TrimSpace(request.CorrelationID),
		ExecutorID:    strings.TrimSpace(request.ExecutorID),
		Service:       strings.TrimSpace(request.Service),
		Artifacts:     map[string]string{},
	}
	for key, value := range finalResult.Artifacts {
		response.Artifacts[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if _, ok := response.Artifacts["service"]; !ok {
		response.Artifacts["service"] = strings.TrimSpace(request.Service)
	}
	if _, ok := response.Artifacts["mode"]; !ok {
		response.Artifacts["mode"] = string(contracts.RunnerModeReview)
	}

	rewriteResult := buildStructuredTaskRewriteResult(finalResult, selectedModel, policyReason)
	response.RewriteResult = &rewriteResult
	response.Artifacts["rewrite_task_description"] = rewriteResult.RevisedTaskDescription
	response.Artifacts["rewrite_acceptance_criteria"] = strings.Join(rewriteResult.RevisedAcceptanceCriteria, "\n")
	response.Artifacts["rewrite_assumptions"] = strings.Join(rewriteResult.Assumptions, "\n")
	response.Artifacts["rewrite_rationale"] = rewriteResult.Rationale
	if strings.TrimSpace(rewriteResult.SelectedModel) != "" {
		response.Artifacts["rewrite_selected_model"] = strings.TrimSpace(rewriteResult.SelectedModel)
	}
	if strings.TrimSpace(rewriteResult.PolicyReason) != "" {
		response.Artifacts["rewrite_policy_reason"] = strings.TrimSpace(rewriteResult.PolicyReason)
	}

	if finalErr != nil {
		response.Error = finalErr.Error()
	} else if finalResult.Status != contracts.RunnerResultCompleted {
		failure := strings.TrimSpace(finalResult.Reason)
		if failure == "" {
			failure = fmt.Sprintf("task rewrite runner returned status %q", finalResult.Status)
		}
		response.Error = failure
		finalErr = fmt.Errorf("%s", failure)
	}
	if h.decisionLogger != nil {
		h.decisionLogger(ctx, TaskRewriteDecisionLog{
			RequestID:     response.RequestID,
			CorrelationID: response.CorrelationID,
			TaskID:        strings.TrimSpace(request.TaskID),
			Service:       response.Service,
			SelectedModel: rewriteResult.SelectedModel,
			PolicyReason:  rewriteResult.PolicyReason,
			Attempts:      attempts,
			Failure:       strings.TrimSpace(response.Error),
		})
	}
	if finalErr != nil {
		return response, finalErr
	}
	return response, nil
}

func buildTaskRewriteRunnerRequest(request ServiceRequestPayload, selectedModel string) contracts.RunnerRequest {
	metadata := cloneStringMap(request.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	runnerRequest := contracts.RunnerRequest{
		TaskID:   strings.TrimSpace(request.TaskID),
		ParentID: strings.TrimSpace(metadata["parent_id"]),
		Prompt:   strings.TrimSpace(metadata["prompt"]),
		Mode:     contracts.RunnerModeReview,
		Model:    strings.TrimSpace(selectedModel),
		RepoRoot: strings.TrimSpace(metadata["repo_root"]),
		Metadata: metadata,
	}
	if runnerRequest.TaskID == "" {
		runnerRequest.TaskID = strings.TrimSpace(request.RequestID)
	}
	if timeoutRaw := strings.TrimSpace(metadata["timeout"]); timeoutRaw != "" {
		if timeout, err := time.ParseDuration(timeoutRaw); err == nil {
			runnerRequest.Timeout = timeout
		}
	}
	return runnerRequest
}

func buildStructuredTaskRewriteResult(result contracts.RunnerResult, selectedModel string, policyReason string) TaskRewriteResult {
	description := firstNonEmptyArtifact(result.Artifacts, "rewrite_task_description", "revised_task_description", "task_description")
	criteria := splitListArtifact(firstNonEmptyArtifact(result.Artifacts, "rewrite_acceptance_criteria", "revised_acceptance_criteria", "acceptance_criteria"))
	assumptions := splitListArtifact(firstNonEmptyArtifact(result.Artifacts, "rewrite_assumptions", "assumptions"))
	rationale := firstNonEmptyArtifact(result.Artifacts, "rewrite_rationale", "rewrite_changes_rationale", "what_changed_and_why")
	return TaskRewriteResult{
		RevisedTaskDescription:    description,
		RevisedAcceptanceCriteria: criteria,
		Assumptions:               assumptions,
		Rationale:                 rationale,
		SelectedModel:             strings.TrimSpace(selectedModel),
		PolicyReason:              strings.TrimSpace(policyReason),
	}
}

func firstNonEmptyArtifact(artifacts map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(artifacts[key]); value != "" {
			return value
		}
	}
	return ""
}

func splitListArtifact(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	lines := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ';'
	})
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		item := strings.TrimSpace(line)
		item = strings.TrimLeft(item, "-*0123456789. ")
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return []string{raw}
	}
	return out
}

func isTaskRewriteServiceName(service string) bool {
	return service == ServiceNameTaskRewrite || service == string(CapabilityRewriteTask) || service == "rewrite-task"
}
