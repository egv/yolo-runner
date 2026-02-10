package opencode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type runWithACPFunc func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpClient ACPClient) error

type CLIRunnerAdapter struct {
	runner     Runner
	acpClient  ACPClient
	configRoot string
	configDir  string
	runWithACP runWithACPFunc
}

var structuredReviewVerdictPattern = regexp.MustCompile(`(?i)\bREVIEW_VERDICT\s*:\s*(pass|fail)\b(?:\s|\\|$|[.,!?"'])`)

func NewCLIRunnerAdapter(runner Runner, acpClient ACPClient, configRoot string, configDir string) *CLIRunnerAdapter {
	return &CLIRunnerAdapter{
		runner:     runner,
		acpClient:  acpClient,
		configRoot: configRoot,
		configDir:  configDir,
		runWithACP: RunWithACP,
	}
}

func (a *CLIRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	start := time.Now().UTC()
	logPath := ""
	if request.Metadata != nil {
		logPath = request.Metadata["log_path"]
	}
	if logPath == "" && request.RepoRoot != "" && request.TaskID != "" {
		logPath = filepath.Join(request.RepoRoot, "runner-logs", "opencode", request.TaskID+".jsonl")
	}

	run := a.runWithACP
	if run == nil {
		run = RunWithACP
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	err := run(runCtx, request.TaskID, request.RepoRoot, request.Prompt, request.Model, a.configRoot, a.configDir, logPath, a.runner, a.acpClient)

	result := contracts.RunnerResult{
		LogPath:    logPath,
		StartedAt:  start,
		FinishedAt: time.Now().UTC(),
	}

	if err == nil {
		result.Status = contracts.RunnerResultCompleted
		if request.Mode == contracts.RunnerModeReview {
			result.ReviewReady = hasStructuredPassVerdict(logPath)
		}
		return result, nil
	}

	var stallErr *StallError
	var verifyErr *VerificationError
	if errors.As(err, &stallErr) || errors.As(err, &verifyErr) {
		result.Status = contracts.RunnerResultBlocked
		result.Reason = err.Error()
		return result, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.Status = contracts.RunnerResultBlocked
		result.Reason = fmt.Sprintf("runner timeout after %s", request.Timeout)
		return result, nil
	}

	result.Status = contracts.RunnerResultFailed
	result.Reason = err.Error()
	return result, nil
}

func hasStructuredPassVerdict(logPath string) bool {
	if strings.TrimSpace(logPath) == "" {
		return false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	matches := structuredReviewVerdictPattern.FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		return false
	}
	last := matches[len(matches)-1]
	if len(last) < 2 {
		return false
	}
	return strings.EqualFold(last[1], "pass")
}
