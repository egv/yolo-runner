package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

var structuredReviewVerdictLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_VERDICT\s*:\s*(pass|fail)(?:\s*DONE)?\s*$`)
var structuredReviewFailFeedbackLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_(?:FAIL_)?FEEDBACK\s*:\s*(.+?)\s*$`)
var tokenRedactionPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`)

const (
	watchdogTimeoutMetadataKey  = "watchdog_timeout"
	watchdogIntervalMetadataKey = "watchdog_interval"
	watchdogLogDirMetadataKey   = "watchdog_opencode_log_dir"
)

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
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		return contracts.RunnerResult{}, errors.New("nil opencode runner adapter")
	}

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
	runCtx = withWatchdogRuntimeConfig(runCtx, watchdogRuntimeConfigFromMetadata(request.Metadata))
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
	if err == nil && runCtx.Err() != nil {
		err = runCtx.Err()
	}

	result := contracts.NormalizeBackendRunnerResult(start, time.Now().UTC(), request, err, func(classifyErr error) bool {
		var stallErr *StallError
		var verifyErr *VerificationError
		return errors.As(classifyErr, &stallErr) || errors.As(classifyErr, &verifyErr)
	})
	result.Artifacts = buildRunnerArtifacts(request, result, err, logPath)
	result.LogPath = logPath
	if result.Status == contracts.RunnerResultCompleted && request.Mode == contracts.RunnerModeReview {
		result.ReviewReady = hasStructuredPassVerdict(logPath)
	}
	return result, nil
}

func buildRunnerArtifacts(request contracts.RunnerRequest, result contracts.RunnerResult, runErr error, logPath string) map[string]string {
	artifacts := map[string]string{}
	if strings.TrimSpace(logPath) != "" {
		artifacts["log_path"] = logPath
	}
	if strings.TrimSpace(request.Model) != "" {
		artifacts["model"] = request.Model
	}
	if strings.TrimSpace(string(request.Mode)) != "" {
		artifacts["mode"] = string(request.Mode)
	}
	if request.Mode == contracts.RunnerModeReview {
		if verdict, ok := structuredReviewVerdict(logPath); ok {
			artifacts["review_verdict"] = verdict
			if verdict == "fail" {
				if feedback, ok := structuredReviewFailFeedback(logPath); ok {
					artifacts["review_fail_feedback"] = feedback
				}
			}
		}
	}
	artifacts["backend"] = "opencode"
	if !result.StartedAt.IsZero() {
		artifacts["started_at"] = result.StartedAt.UTC().Format(time.RFC3339)
	}
	if !result.FinishedAt.IsZero() {
		artifacts["finished_at"] = result.FinishedAt.UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(result.Reason) != "" {
		artifacts["reason"] = result.Reason
	}
	artifacts["status"] = string(result.Status)

	var stallErr *StallError
	if errors.As(runErr, &stallErr) {
		if strings.TrimSpace(stallErr.Category) != "" {
			artifacts["stall_category"] = stallErr.Category
		}
		if strings.TrimSpace(stallErr.SessionID) != "" {
			artifacts["session_id"] = stallErr.SessionID
		}
		if stallErr.LastOutputAge > 0 {
			artifacts["last_output_age"] = stallErr.LastOutputAge.Round(time.Second).String()
		}
		if strings.TrimSpace(stallErr.OpenCodeLog) != "" {
			artifacts["opencode_log"] = stallErr.OpenCodeLog
		}
		if strings.TrimSpace(stallErr.TailPath) != "" {
			artifacts["opencode_tail_path"] = stallErr.TailPath
		}
	}

	if request.Metadata != nil {
		for _, key := range []string{"clone_path"} {
			if value := strings.TrimSpace(request.Metadata[key]); value != "" {
				artifacts[key] = value
			}
		}
	}

	if len(artifacts) == 0 {
		return nil
	}
	return artifacts
}

func watchdogRuntimeConfigFromMetadata(metadata map[string]string) watchdogRuntimeConfig {
	if len(metadata) == 0 {
		return watchdogRuntimeConfig{}
	}
	config := watchdogRuntimeConfig{}
	if raw := strings.TrimSpace(metadata[watchdogTimeoutMetadataKey]); raw != "" {
		if parsed, ok := parseWatchdogDuration(raw); ok {
			config.Timeout = parsed
		}
	}
	if raw := strings.TrimSpace(metadata[watchdogIntervalMetadataKey]); raw != "" {
		if parsed, ok := parseWatchdogDuration(raw); ok {
			config.Interval = parsed
		}
	}
	if raw := strings.TrimSpace(metadata[watchdogLogDirMetadataKey]); raw != "" {
		config.OpenCodeLogDir = raw
	}
	return config
}

func parseWatchdogDuration(raw string) (time.Duration, bool) {
	parsed, err := time.ParseDuration(raw)
	if err == nil {
		return parsed, true
	}
	if seconds, convErr := strconv.Atoi(raw); convErr == nil {
		return time.Duration(seconds) * time.Second, true
	}
	return 0, false
}

func hasStructuredPassVerdict(logPath string) bool {
	verdict, ok := structuredReviewVerdict(logPath)
	if !ok {
		return false
	}
	return strings.EqualFold(verdict, "pass")
}

func structuredReviewVerdict(logPath string) (string, bool) {
	if strings.TrimSpace(logPath) == "" {
		return "", false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return "", false
	}
	return lastStructuredVerdictFromACPLog(content)
}

func structuredReviewFailFeedback(logPath string) (string, bool) {
	if strings.TrimSpace(logPath) == "" {
		return "", false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return "", false
	}
	return lastStructuredReviewFailFeedbackFromACPLog(content)
}

func lastStructuredVerdictFromACPLog(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	var combinedAgentMessages strings.Builder
	for scanner.Scan() {
		text := extractAgentMessageText(scanner.Text())
		if text == "" {
			continue
		}
		combinedAgentMessages.WriteString(text)
	}

	if scanner.Err() != nil {
		return "", false
	}

	return lastStructuredVerdictLine(combinedAgentMessages.String())
}

func lastStructuredReviewFailFeedbackFromACPLog(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	var combinedAgentMessages strings.Builder
	for scanner.Scan() {
		text := extractAgentMessageText(scanner.Text())
		if text == "" {
			continue
		}
		combinedAgentMessages.WriteString(text)
	}

	if scanner.Err() != nil {
		return "", false
	}

	return lastStructuredReviewFailFeedbackLine(combinedAgentMessages.String())
}

func extractAgentMessageText(logLine string) string {
	line := strings.TrimSpace(logLine)
	if line == "" {
		return ""
	}

	entry := struct {
		Message string `json:"message"`
	}{}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return ""
	}

	message := strings.TrimSpace(entry.Message)
	if message == "" || !strings.HasPrefix(message, "agent_message") {
		return ""
	}

	payload := strings.TrimSpace(strings.TrimPrefix(message, "agent_message"))
	if payload == "" {
		return ""
	}
	if strings.HasPrefix(payload, `"`) {
		if unquoted, err := strconv.Unquote(payload); err == nil {
			return unquoted
		}
	}
	return payload
}

func lastStructuredVerdictLine(text string) (string, bool) {
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	if normalized == "" {
		return "", false
	}

	lastVerdict := ""
	found := false
	for _, line := range strings.Split(normalized, "\n") {
		matches := structuredReviewVerdictLinePattern.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}
		lastVerdict = strings.ToLower(matches[1])
		found = true
	}

	return lastVerdict, found
}

func lastStructuredReviewFailFeedbackLine(text string) (string, bool) {
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	if normalized == "" {
		return "", false
	}

	lastFeedback := ""
	found := false
	for _, line := range strings.Split(normalized, "\n") {
		matches := structuredReviewFailFeedbackLinePattern.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}
		candidate := strings.Join(strings.Fields(matches[1]), " ")
		if candidate == "" {
			continue
		}
		lastFeedback = candidate
		found = true
	}

	return lastFeedback, found
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
