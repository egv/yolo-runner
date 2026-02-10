package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type runWithACPFunc func(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string, runner Runner, acpClient ACPClient, onUpdate func(string)) error

type CLIRunnerAdapter struct {
	runner     Runner
	acpClient  ACPClient
	configRoot string
	configDir  string
	runWithACP runWithACPFunc
}

var structuredReviewVerdictPattern = regexp.MustCompile(`(?i)\bREVIEW_VERDICT\s*:\s*(pass|fail)\b(?:\s|\\|$|[.,!?"'])`)
var tokenRedactionPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`)

func NewCLIRunnerAdapter(runner Runner, acpClient ACPClient, configRoot string, configDir string) *CLIRunnerAdapter {
	return &CLIRunnerAdapter{
		runner:     runner,
		acpClient:  acpClient,
		configRoot: configRoot,
		configDir:  configDir,
		runWithACP: RunWithACPAndUpdates,
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
		run = RunWithACPAndUpdates
	}
	progress := request.OnProgress
	runCtx := ctx
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	err := run(runCtx, request.TaskID, request.RepoRoot, request.Prompt, request.Model, a.configRoot, a.configDir, logPath, a.runner, a.acpClient, func(line string) {
		if progress == nil {
			return
		}
		normalized, progressType := normalizeACPUpdateLine(line)
		if strings.TrimSpace(normalized) == "" {
			return
		}
		progress(contracts.RunnerProgress{Type: progressType, Message: normalized, Timestamp: time.Now().UTC()})
	})

	result := contracts.NormalizeBackendRunnerResult(start, time.Now().UTC(), request, err, func(classifyErr error) bool {
		var stallErr *StallError
		var verifyErr *VerificationError
		return errors.As(classifyErr, &stallErr) || errors.As(classifyErr, &verifyErr)
	})
	result.LogPath = logPath
	if result.Status == contracts.RunnerResultCompleted && request.Mode == contracts.RunnerModeReview {
		result.ReviewReady = hasStructuredPassVerdict(logPath)
	}
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

func normalizeACPUpdateLine(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", "runner_output"
	}
	typeName := "runner_output"
	switch {
	case strings.HasPrefix(trimmed, "⏳"), strings.HasPrefix(trimmed, "🔄"):
		typeName = "runner_cmd_started"
	case strings.HasPrefix(trimmed, "✅"), strings.HasPrefix(trimmed, "❌"):
		typeName = "runner_cmd_finished"
	case strings.HasPrefix(trimmed, "⚪"), strings.HasPrefix(trimmed, "request permission"):
		typeName = "runner_warning"
	}
	trimmed = strings.ReplaceAll(trimmed, "\r", "")
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	trimmed = tokenRedactionPattern.ReplaceAllString(trimmed, "<redacted-token>")
	const maxLen = 500
	if len(trimmed) > maxLen {
		trimmed = trimmed[:maxLen] + "..."
	}
	return trimmed, typeName
}
