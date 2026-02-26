package distributed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type ReviewVerdict string

const (
	ReviewVerdictPass ReviewVerdict = "pass"
	ReviewVerdictFail ReviewVerdict = "fail"
)

type ReviewResult struct {
	Pass             bool          `json:"pass"`
	Verdict          ReviewVerdict `json:"verdict"`
	BlockingFeedback string        `json:"blocking_feedback,omitempty"`
	SelectedModel    string        `json:"selected_model,omitempty"`
	PolicyReason     string        `json:"policy_reason,omitempty"`
}

type ReviewDecisionLog struct {
	RequestID     string
	CorrelationID string
	TaskID        string
	Service       string
	SelectedModel string
	PolicyReason  string
	Verdict       ReviewVerdict
	Pass          bool
	Attempts      int
	Failure       string
}

type ReviewModelPolicy struct {
	DefaultModel string
	LargerModel  string
}

func (p ReviewModelPolicy) Select(request ServiceRequestPayload) (string, string) {
	metadata := request.Metadata
	if strings.EqualFold(strings.TrimSpace(metadata["review_policy"]), "larger_model") ||
		isLargerModelReviewService(strings.TrimSpace(request.Service)) {
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

type MastermindReviewRequestHandlerOptions struct {
	ReviewRunner       contracts.AgentRunner
	DefaultReviewModel string
	LargerReviewModel  string
	MaxRetries         int
	AttemptTimeout     time.Duration
	RetryDelay         time.Duration
	DecisionLogger     func(context.Context, ReviewDecisionLog)
}

type MastermindReviewRequestHandler struct {
	reviewRunner   contracts.AgentRunner
	modelPolicy    ReviewModelPolicy
	maxRetries     int
	attemptTimeout time.Duration
	retryDelay     time.Duration
	decisionLogger func(context.Context, ReviewDecisionLog)
}

func NewMastermindReviewRequestHandler(opts MastermindReviewRequestHandlerOptions) *MastermindReviewRequestHandler {
	return &MastermindReviewRequestHandler{
		reviewRunner: opts.ReviewRunner,
		modelPolicy: ReviewModelPolicy{
			DefaultModel: strings.TrimSpace(opts.DefaultReviewModel),
			LargerModel:  strings.TrimSpace(opts.LargerReviewModel),
		},
		maxRetries:     max(0, opts.MaxRetries),
		attemptTimeout: opts.AttemptTimeout,
		retryDelay:     opts.RetryDelay,
		decisionLogger: opts.DecisionLogger,
	}
}

func (h *MastermindReviewRequestHandler) Handle(ctx context.Context, request ServiceRequestPayload) (ServiceResponsePayload, error) {
	if h == nil || h.reviewRunner == nil {
		return ServiceResponsePayload{}, fmt.Errorf("review handler unavailable")
	}
	service := normalizeServiceName(request.Service)
	if !isReviewServiceName(service) {
		return ServiceResponsePayload{}, fmt.Errorf("unsupported review service %q", strings.TrimSpace(request.Service))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	selectedModel, policyReason := h.modelPolicy.Select(request)
	runnerRequest := buildReviewRunnerRequest(request, selectedModel)

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
		result, runErr := h.reviewRunner.Run(attemptCtx, runnerRequest)
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

	reviewResult := buildStructuredReviewResult(finalResult, finalErr, selectedModel, policyReason)
	response.ReviewResult = &reviewResult
	response.Artifacts["review_verdict"] = string(reviewResult.Verdict)
	if strings.TrimSpace(reviewResult.BlockingFeedback) != "" {
		response.Artifacts["review_fail_feedback"] = strings.TrimSpace(reviewResult.BlockingFeedback)
	}
	if strings.TrimSpace(reviewResult.SelectedModel) != "" {
		response.Artifacts["review_selected_model"] = strings.TrimSpace(reviewResult.SelectedModel)
	}
	if strings.TrimSpace(reviewResult.PolicyReason) != "" {
		response.Artifacts["review_policy_reason"] = strings.TrimSpace(reviewResult.PolicyReason)
	}

	if finalErr != nil {
		response.Error = finalErr.Error()
	} else if finalResult.Status != contracts.RunnerResultCompleted {
		failure := strings.TrimSpace(finalResult.Reason)
		if failure == "" {
			failure = fmt.Sprintf("review runner returned status %q", finalResult.Status)
		}
		response.Error = failure
		finalErr = fmt.Errorf("%s", failure)
	}
	if h.decisionLogger != nil {
		h.decisionLogger(ctx, ReviewDecisionLog{
			RequestID:     response.RequestID,
			CorrelationID: response.CorrelationID,
			TaskID:        strings.TrimSpace(request.TaskID),
			Service:       response.Service,
			SelectedModel: reviewResult.SelectedModel,
			PolicyReason:  reviewResult.PolicyReason,
			Verdict:       reviewResult.Verdict,
			Pass:          reviewResult.Pass,
			Attempts:      attempts,
			Failure:       strings.TrimSpace(response.Error),
		})
	}
	if finalErr != nil {
		return response, finalErr
	}
	return response, nil
}

func buildReviewRunnerRequest(request ServiceRequestPayload, selectedModel string) contracts.RunnerRequest {
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

func buildStructuredReviewResult(result contracts.RunnerResult, runErr error, selectedModel string, policyReason string) ReviewResult {
	verdict := reviewVerdictFromResult(result, runErr)
	feedback := reviewFeedbackFromResult(result, runErr)
	return ReviewResult{
		Pass:             verdict == ReviewVerdictPass,
		Verdict:          verdict,
		BlockingFeedback: feedback,
		SelectedModel:    strings.TrimSpace(selectedModel),
		PolicyReason:     strings.TrimSpace(policyReason),
	}
}

func reviewVerdictFromResult(result contracts.RunnerResult, runErr error) ReviewVerdict {
	if runErr != nil {
		return ReviewVerdictFail
	}
	artifactVerdict := strings.ToLower(strings.TrimSpace(result.Artifacts["review_verdict"]))
	switch artifactVerdict {
	case string(ReviewVerdictPass):
		return ReviewVerdictPass
	case string(ReviewVerdictFail):
		return ReviewVerdictFail
	}
	if result.Status != contracts.RunnerResultCompleted {
		return ReviewVerdictFail
	}
	return ReviewVerdictPass
}

func reviewFeedbackFromResult(result contracts.RunnerResult, runErr error) string {
	for _, key := range []string{"review_fail_feedback", "review_feedback"} {
		if feedback := strings.TrimSpace(result.Artifacts[key]); feedback != "" {
			return feedback
		}
	}
	if runErr != nil {
		return strings.TrimSpace(runErr.Error())
	}
	if result.Status != contracts.RunnerResultCompleted {
		return strings.TrimSpace(result.Reason)
	}
	return ""
}

func isReviewServiceName(service string) bool {
	return service == ServiceNameReview || isLargerModelReviewService(service)
}

func isLargerModelReviewService(service string) bool {
	return service == "review-with-larger-model" || service == string(CapabilityLargerModel)
}

func normalizeServiceName(raw string) string {
	service := strings.TrimSpace(strings.ToLower(raw))
	return strings.ReplaceAll(service, "_", "-")
}

func contextForReviewAttempt(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
