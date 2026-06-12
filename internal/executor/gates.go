package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	taskquality "github.com/egv/yolo-runner/v2/internal/task_quality"
)

const DefaultQualityGateThreshold = 70

const (
	QualityGateToolTaskValidator     = "task_validator"
	QualityGateToolDependencyChecker = "dependency_checker"
	QCGateToolTestRunner             = "test_runner"
	QCGateToolLinter                 = "linter"
	QCGateToolCoverageChecker        = "coverage_checker"
)

type GateOptions struct {
	RepoRoot             string
	QualityGateThreshold int
	QualityGateTools     []string
	QCGateTools          []string
	AllowLowQuality      bool
	RequireReview        bool
}

type GateEventContext struct {
	WorkerID  string
	ClonePath string
	QueuePos  int
}

type GateDependencies struct {
	Tasks                   contracts.TaskManager
	Events                  contracts.EventSink
	MarkTaskBlockedWithData func(taskID string, taskData map[string]string) error
	ClearTaskTerminalState  func(taskID string) error
}

type QCGateToolResult struct {
	Tool      string `json:"tool"`
	Status    string `json:"status"`
	Passed    bool   `json:"passed"`
	Reason    string `json:"reason,omitempty"`
	Value     string `json:"value,omitempty"`
	Threshold int    `json:"threshold,omitempty"`
	Command   string `json:"command,omitempty"`
	Critical  bool   `json:"critical,omitempty"`
}

type QCGateReport struct {
	Status    string             `json:"status"`
	Tools     []QCGateToolResult `json:"tools"`
	Review    string             `json:"review_verdict,omitempty"`
	Threshold int                `json:"threshold"`
}

func RunQualityGate(ctx context.Context, task contracts.Task, deps GateDependencies, options GateOptions, eventContext GateEventContext) (bool, error) {
	tools, err := resolveQualityGateTools(options.QualityGateTools)
	if err != nil {
		return false, err
	}

	if len(tools) > 0 {
		qualityThreshold := options.QualityGateThreshold
		if qualityThreshold <= 0 {
			qualityThreshold = DefaultQualityGateThreshold
		}

		qualityScore, qualityIssues, ok, err := evaluateTaskQuality(ctx, deps.Tasks, task, tools)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}

		qualityMetadata := map[string]string{
			"quality_score":     strconv.Itoa(qualityScore),
			"quality_threshold": strconv.Itoa(qualityThreshold),
			"quality_gate":      "true",
			"quality_issues":    strings.Join(qualityIssues, "\n"),
		}
		qualityMetadata["quality_gate_comment"] = qualityGateComment(qualityMetadata, qualityScore, qualityThreshold)

		if qualityScore >= qualityThreshold {
			return false, nil
		}

		qualityGateReason := fmt.Sprintf("quality score %d is below threshold %d", qualityScore, qualityThreshold)
		if options.AllowLowQuality {
			warningMetadata := map[string]string{
				"quality_threshold": strconv.Itoa(qualityThreshold),
				"quality_score":     strconv.Itoa(qualityScore),
				"reason":            qualityGateReason,
			}
			for key, value := range qualityMetadata {
				warningMetadata[key] = value
			}
			_ = emitGateEvent(ctx, deps.Events, contracts.Event{
				Type:      contracts.EventTypeRunnerWarning,
				TaskID:    task.ID,
				TaskTitle: task.Title,
				WorkerID:  eventContext.WorkerID,
				ClonePath: eventContext.ClonePath,
				QueuePos:  eventContext.QueuePos,
				Message:   "quality gate threshold overridden by --allow-low-quality",
				Metadata:  warningMetadata,
				Timestamp: time.Now().UTC(),
			})
			return false, nil
		}

		blockedData := map[string]string{
			"triage_status":        "blocked",
			"triage_reason":        qualityGateReason,
			"quality_score":        strconv.Itoa(qualityScore),
			"quality_threshold":    strconv.Itoa(qualityThreshold),
			"quality_gate":         "true",
			"quality_gate_comment": qualityMetadata["quality_gate_comment"],
		}
		for key, value := range qualityMetadata {
			blockedData[key] = value
		}
		blockedData = appendGateDecisionMetadata(blockedData, "blocked", qualityGateReason)
		if err := markTaskBlockedWithData(deps, task.ID, blockedData); err != nil {
			return false, err
		}
		if err := setGateTaskStatus(ctx, deps, task.ID, contracts.TaskStatusBlocked); err != nil {
			return false, err
		}

		finishedMetadata := map[string]string{
			"triage_status":     "blocked",
			"triage_reason":     qualityGateReason,
			"quality_score":     strconv.Itoa(qualityScore),
			"quality_threshold": strconv.Itoa(qualityThreshold),
			"quality_gate":      "true",
		}
		finishedMetadata = appendGateDecisionMetadata(finishedMetadata, "blocked", qualityGateReason)
		_ = emitGateEvent(ctx, deps.Events, contracts.Event{
			Type:      contracts.EventTypeTaskFinished,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  eventContext.WorkerID,
			ClonePath: eventContext.ClonePath,
			QueuePos:  eventContext.QueuePos,
			Message:   string(contracts.TaskStatusBlocked),
			Metadata:  finishedMetadata,
			Timestamp: time.Now().UTC(),
		})
		if err := setGateTaskData(ctx, deps, task.ID, blockedData); err != nil {
			return false, err
		}
		_ = emitGateEvent(ctx, deps.Events, contracts.Event{
			Type:      contracts.EventTypeTaskDataUpdated,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  eventContext.WorkerID,
			ClonePath: eventContext.ClonePath,
			QueuePos:  eventContext.QueuePos,
			Metadata:  blockedData,
			Timestamp: time.Now().UTC(),
		})
		if err := clearTaskTerminalState(deps, task.ID); err != nil {
			return false, err
		}
		return true, nil
	}

	qualityThreshold := options.QualityGateThreshold
	if qualityThreshold <= 0 {
		qualityThreshold = DefaultQualityGateThreshold
	}

	qualityScore, ok := taskExecutionThresholdScore(task.Metadata)
	if !ok || qualityScore >= qualityThreshold {
		return false, nil
	}

	qualityGateReason := fmt.Sprintf("quality score %d is below threshold %d", qualityScore, qualityThreshold)
	qualityComment := qualityGateComment(task.Metadata, qualityScore, qualityThreshold)
	qualityMetadata := map[string]string{
		"quality_score":        strconv.Itoa(qualityScore),
		"quality_threshold":    strconv.Itoa(qualityThreshold),
		"quality_gate":         "true",
		"quality_gate_comment": qualityComment,
	}
	if options.AllowLowQuality {
		warningMetadata := map[string]string{
			"quality_threshold": strconv.Itoa(qualityThreshold),
			"quality_score":     strconv.Itoa(qualityScore),
			"reason":            qualityGateReason,
		}
		for key, value := range qualityMetadata {
			warningMetadata[key] = value
		}
		_ = emitGateEvent(ctx, deps.Events, contracts.Event{
			Type:      contracts.EventTypeRunnerWarning,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  eventContext.WorkerID,
			ClonePath: eventContext.ClonePath,
			QueuePos:  eventContext.QueuePos,
			Message:   "quality gate threshold overridden by --allow-low-quality",
			Metadata:  warningMetadata,
			Timestamp: time.Now().UTC(),
		})
		return false, nil
	}

	blockedData := map[string]string{
		"triage_status":        "blocked",
		"triage_reason":        qualityGateReason,
		"quality_score":        strconv.Itoa(qualityScore),
		"quality_threshold":    strconv.Itoa(qualityThreshold),
		"quality_gate":         "true",
		"quality_gate_comment": qualityComment,
	}
	blockedData = appendGateDecisionMetadata(blockedData, "blocked", qualityGateReason)
	if err := markTaskBlockedWithData(deps, task.ID, blockedData); err != nil {
		return false, err
	}
	if err := setGateTaskStatus(ctx, deps, task.ID, contracts.TaskStatusBlocked); err != nil {
		return false, err
	}
	finishedMetadata := map[string]string{
		"triage_status":     "blocked",
		"triage_reason":     qualityGateReason,
		"quality_score":     strconv.Itoa(qualityScore),
		"quality_threshold": strconv.Itoa(qualityThreshold),
		"quality_gate":      "true",
	}
	finishedMetadata = appendGateDecisionMetadata(finishedMetadata, "blocked", qualityGateReason)
	_ = emitGateEvent(ctx, deps.Events, contracts.Event{
		Type:      contracts.EventTypeTaskFinished,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  eventContext.WorkerID,
		ClonePath: eventContext.ClonePath,
		QueuePos:  eventContext.QueuePos,
		Message:   string(contracts.TaskStatusBlocked),
		Metadata:  finishedMetadata,
		Timestamp: time.Now().UTC(),
	})
	if err := setGateTaskData(ctx, deps, task.ID, blockedData); err != nil {
		return false, err
	}
	_ = emitGateEvent(ctx, deps.Events, contracts.Event{
		Type:      contracts.EventTypeTaskDataUpdated,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  eventContext.WorkerID,
		ClonePath: eventContext.ClonePath,
		QueuePos:  eventContext.QueuePos,
		Metadata:  blockedData,
		Timestamp: time.Now().UTC(),
	})
	if err := clearTaskTerminalState(deps, task.ID); err != nil {
		return false, err
	}
	return true, nil
}

func RunQCGate(ctx context.Context, task contracts.Task, result contracts.RunnerResult, deps GateDependencies, options GateOptions, eventContext GateEventContext) (bool, error) {
	tools, err := resolveQCGateTools(options.QCGateTools)
	if err != nil {
		return false, err
	}
	if len(tools) == 0 && !(options.RequireReview && result.ReviewReady) {
		return false, nil
	}

	repoRoot := strings.TrimSpace(eventContext.ClonePath)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(options.RepoRoot)
	}

	qcThreshold := options.QualityGateThreshold
	if qcThreshold <= 0 {
		qcThreshold = DefaultQualityGateThreshold
	}

	outcomes := make([]QCGateToolResult, 0, len(tools)+1)
	failed := []string{}
	for _, tool := range tools {
		var outcome QCGateToolResult
		switch tool {
		case QCGateToolTestRunner:
			outcome = RunQCTestSuiteValidation(ctx, repoRoot)
		case QCGateToolLinter:
			outcome = RunQCLinterValidation(ctx, repoRoot)
		case QCGateToolCoverageChecker:
			outcome = RunQCCoverageValidation(ctx, repoRoot, qcThreshold)
		default:
			return false, fmt.Errorf("unsupported quality control gate tool %q", tool)
		}
		outcomes = append(outcomes, outcome)
		if outcome.Critical {
			return false, fmt.Errorf("quality control gate tool %q failed critically: %s", tool, outcome.Reason)
		}
		if !outcome.Passed {
			failed = append(failed, outcome.Reason)
		}
	}

	reviewApproval := QCGateToolResult{}
	if options.RequireReview && result.ReviewReady {
		reviewApproval = RunQCReviewApproval(options.RequireReview, result)
		outcomes = append(outcomes, reviewApproval)
		if !reviewApproval.Passed {
			failed = append(failed, reviewApproval.Reason)
		}
	}
	if len(tools) == 0 && reviewApproval.Tool != "" && reviewApproval.Passed {
		return false, nil
	}

	report := QCGateReport{
		Status:    "passed",
		Threshold: qcThreshold,
		Tools:     outcomes,
	}
	if reviewApproval.Tool != "" {
		report.Review = reviewApproval.Value
	}
	if len(failed) > 0 {
		report.Status = "failed"
	}

	reportJSON, err := json.Marshal(report)
	if err != nil {
		return false, fmt.Errorf("marshal quality control gate report: %w", err)
	}

	qcMetadata := map[string]string{
		"qc_gate":           "true",
		"qc_gate_status":    report.Status,
		"qc_gate_threshold": strconv.Itoa(qcThreshold),
		"qc_gate_tools":     strings.Join(tools, ","),
		"qc_gate_report":    string(reportJSON),
	}
	for _, outcome := range outcomes {
		keyPrefix := "qc_" + strings.ReplaceAll(strings.ReplaceAll(outcome.Tool, "-", "_"), " ", "_")
		qcMetadata[keyPrefix+"_status"] = outcome.Status
		if strings.TrimSpace(outcome.Value) != "" {
			qcMetadata[keyPrefix+"_value"] = outcome.Value
		}
		if strings.TrimSpace(outcome.Reason) != "" {
			qcMetadata[keyPrefix+"_reason"] = outcome.Reason
		}
	}

	if len(failed) == 0 {
		if err := setGateTaskData(ctx, deps, task.ID, qcMetadata); err != nil {
			return false, err
		}
		_ = emitGateEvent(ctx, deps.Events, contracts.Event{
			Type:      contracts.EventTypeTaskDataUpdated,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  eventContext.WorkerID,
			ClonePath: eventContext.ClonePath,
			QueuePos:  eventContext.QueuePos,
			Metadata:  qcMetadata,
			Timestamp: time.Now().UTC(),
		})
		return false, nil
	}

	blockedReason := "quality control gate failed: " + strings.Join(failed, "; ")
	blockedData := map[string]string{
		"triage_status":     "blocked",
		"triage_reason":     blockedReason,
		"qc_gate":           "true",
		"qc_gate_status":    report.Status,
		"qc_gate_threshold": strconv.Itoa(qcThreshold),
		"qc_gate_tools":     strings.Join(tools, ","),
		"qc_gate_report":    string(reportJSON),
	}
	blockedData = appendGateDecisionMetadata(blockedData, "blocked", blockedReason)
	for key, value := range qcMetadata {
		blockedData[key] = value
	}
	if err := markTaskBlockedWithData(deps, task.ID, blockedData); err != nil {
		return false, err
	}
	if err := setGateTaskStatus(ctx, deps, task.ID, contracts.TaskStatusBlocked); err != nil {
		return false, err
	}
	finishedMetadata := map[string]string{
		"triage_status":     "blocked",
		"triage_reason":     blockedReason,
		"qc_gate":           "true",
		"qc_gate_status":    report.Status,
		"qc_gate_threshold": strconv.Itoa(qcThreshold),
		"qc_gate_tools":     strings.Join(tools, ","),
	}
	finishedMetadata = appendGateDecisionMetadata(finishedMetadata, "blocked", blockedReason)
	for key, value := range qcMetadata {
		finishedMetadata[key] = value
	}
	_ = emitGateEvent(ctx, deps.Events, contracts.Event{
		Type:      contracts.EventTypeTaskFinished,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  eventContext.WorkerID,
		ClonePath: eventContext.ClonePath,
		QueuePos:  eventContext.QueuePos,
		Message:   string(contracts.TaskStatusBlocked),
		Metadata:  finishedMetadata,
		Timestamp: time.Now().UTC(),
	})
	if err := setGateTaskData(ctx, deps, task.ID, blockedData); err != nil {
		return false, err
	}
	_ = emitGateEvent(ctx, deps.Events, contracts.Event{
		Type:      contracts.EventTypeTaskDataUpdated,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  eventContext.WorkerID,
		ClonePath: eventContext.ClonePath,
		QueuePos:  eventContext.QueuePos,
		Metadata:  blockedData,
		Timestamp: time.Now().UTC(),
	})
	if err := clearTaskTerminalState(deps, task.ID); err != nil {
		return false, err
	}
	return true, nil
}

func RunQCTestSuiteValidation(ctx context.Context, repoRoot string) QCGateToolResult {
	output, err := runQCGateCommand(ctx, repoRoot, "go", "test", "./...")
	result := QCGateToolResult{
		Tool:    QCGateToolTestRunner,
		Command: "go test ./...",
	}
	if err == nil {
		result.Passed = true
		result.Status = "passed"
		result.Value = "passed"
		return result
	}
	if _, ok := err.(*exec.ExitError); ok {
		result.Passed = false
		result.Status = "failed"
		result.Reason = strings.TrimSpace(firstNonEmptyLine(output))
		if result.Reason == "" {
			result.Reason = "test suite returned non-zero status"
		}
		return result
	}
	result.Critical = true
	result.Status = "critical_error"
	result.Reason = strings.TrimSpace(err.Error())
	if result.Reason == "" {
		result.Reason = "unable to execute test suite command"
	}
	return result
}

func RunQCLinterValidation(ctx context.Context, repoRoot string) QCGateToolResult {
	output, err := runQCGateCommand(ctx, repoRoot, "go", "vet", "./...")
	result := QCGateToolResult{
		Tool:    QCGateToolLinter,
		Command: "go vet ./...",
	}
	if err == nil {
		result.Passed = true
		result.Status = "passed"
		result.Value = "passed"
		return result
	}
	if _, ok := err.(*exec.ExitError); ok {
		result.Passed = false
		result.Status = "failed"
		result.Reason = strings.TrimSpace(firstNonEmptyLine(output))
		if result.Reason == "" {
			result.Reason = "linter returned non-zero status"
		}
		return result
	}
	result.Critical = true
	result.Status = "critical_error"
	result.Reason = strings.TrimSpace(err.Error())
	if result.Reason == "" {
		result.Reason = "unable to execute linter command"
	}
	return result
}

func RunQCCoverageValidation(ctx context.Context, repoRoot string, threshold int) QCGateToolResult {
	profileFile, err := os.CreateTemp("", "yolo-runner-qc-coverage-*.out")
	if err != nil {
		return QCGateToolResult{
			Tool:     QCGateToolCoverageChecker,
			Status:   "critical_error",
			Critical: true,
			Reason:   "failed to create coverage profile temp file",
		}
	}
	profilePath := profileFile.Name()
	_ = profileFile.Close()
	defer func() {
		_ = os.Remove(profilePath)
	}()

	_, runErr := runQCGateCommand(ctx, repoRoot, "go", "test", "./...", "-coverprofile="+profilePath)
	result := QCGateToolResult{
		Tool:      QCGateToolCoverageChecker,
		Command:   "go test ./... -coverprofile=<tmp>",
		Threshold: threshold,
	}
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); ok {
			result.Passed = false
			result.Status = "failed"
			result.Reason = "coverage test execution failed"
			return result
		}
		result.Critical = true
		result.Status = "critical_error"
		result.Reason = strings.TrimSpace(runErr.Error())
		if result.Reason == "" {
			result.Reason = "unable to execute coverage test command"
		}
		return result
	}

	coverageOutput, err := runQCGateCommand(ctx, repoRoot, "go", "tool", "cover", "-func="+profilePath)
	if err != nil {
		result.Critical = true
		result.Status = "critical_error"
		result.Reason = "failed to collect coverage report"
		return result
	}

	coverage, parseErr := parseCoveragePercentFromReport(coverageOutput)
	if parseErr != nil {
		result.Critical = true
		result.Status = "critical_error"
		result.Reason = parseErr.Error()
		return result
	}
	result.Value = strconv.FormatFloat(coverage, 'f', 1, 64)
	if coverage < float64(threshold) {
		result.Passed = false
		result.Status = "failed"
		result.Reason = fmt.Sprintf("coverage %.1f is below threshold %d", coverage, threshold)
		return result
	}
	result.Passed = true
	result.Status = "passed"
	result.Value = fmt.Sprintf("%.1f", coverage)
	return result
}

func RunQCReviewApproval(requireReview bool, result contracts.RunnerResult) QCGateToolResult {
	outcome := QCGateToolResult{
		Tool:    "review_approval",
		Command: "review approval check",
	}
	if !requireReview {
		outcome.Status = "not_required"
		outcome.Passed = true
		outcome.Reason = "not_required"
		outcome.Value = "skipped"
		return outcome
	}

	if result.Status != contracts.RunnerResultCompleted {
		outcome.Status = "failed"
		outcome.Passed = false
		outcome.Value = "not_approved"
		outcome.Reason = "review result was not completed"
		return outcome
	}
	if !result.ReviewReady {
		outcome.Status = "failed"
		outcome.Passed = false
		outcome.Value = "not_approved"
		outcome.Reason = "review not ready"
		return outcome
	}

	verdict := ReviewVerdictFromArtifacts(result)
	if verdict == "pass" {
		outcome.Status = "passed"
		outcome.Passed = true
		outcome.Value = "pass"
		return outcome
	}
	if verdict == "fail" {
		outcome.Status = "failed"
		outcome.Passed = false
		outcome.Value = "fail"
		if feedback := strings.TrimSpace(ReviewFailFeedbackFromArtifacts(result)); feedback != "" {
			outcome.Reason = feedback
		} else {
			outcome.Reason = "review verdict returned fail"
		}
		return outcome
	}
	outcome.Status = "failed"
	outcome.Passed = false
	outcome.Value = "not_approved"
	if feedback := strings.TrimSpace(ReviewFailFeedbackFromArtifacts(result)); feedback != "" {
		outcome.Reason = feedback
		return outcome
	}
	outcome.Reason = "review verdict missing"
	return outcome
}

func ReviewVerdictFromArtifacts(result contracts.RunnerResult) string {
	if len(result.Artifacts) == 0 {
		return ""
	}
	verdict := strings.ToLower(strings.TrimSpace(result.Artifacts["review_verdict"]))
	if verdict == "pass" || verdict == "fail" {
		return verdict
	}
	return ""
}

func ReviewFailFeedbackFromArtifacts(result contracts.RunnerResult) string {
	if len(result.Artifacts) == 0 {
		return ""
	}
	for _, key := range []string{"review_fail_feedback", "review_feedback"} {
		value := strings.TrimSpace(result.Artifacts[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func runQCGateCommand(ctx context.Context, repoRoot string, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if strings.TrimSpace(repoRoot) != "" {
		cmd.Dir = strings.TrimSpace(repoRoot)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func parseCoveragePercentFromReport(rawReport string) (float64, error) {
	for _, line := range strings.Split(rawReport, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "total:") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if !strings.HasSuffix(field, "%") {
				continue
			}
			value := strings.TrimSuffix(strings.TrimSpace(field), "%")
			if value == "" {
				continue
			}
			score, err := strconv.ParseFloat(value, 64)
			if err != nil {
				continue
			}
			return score, nil
		}
	}
	return 0, fmt.Errorf("coverage report is missing total percentage line")
}

func firstNonEmptyLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func resolveQCGateTools(rawTools []string) ([]string, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}

	tools := make([]string, 0, len(rawTools))
	seen := map[string]struct{}{}
	for _, tool := range rawTools {
		tool = strings.ToLower(strings.TrimSpace(tool))
		if tool == "" {
			continue
		}
		switch tool {
		case QCGateToolTestRunner, QCGateToolLinter, QCGateToolCoverageChecker:
		default:
			return nil, fmt.Errorf("unsupported quality control gate tool %q", tool)
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools, nil
}

func evaluateTaskQuality(ctx context.Context, tasks contracts.TaskManager, task contracts.Task, tools []string) (int, []string, bool, error) {
	qualityScore, hasScore := 0, false
	issues := []string{}

	if score, ok := taskQualityScore(task.Metadata); ok {
		qualityScore = score
		hasScore = true
		issues = append(issues, qualityGateIssues(task.Metadata)...)
	}

	if hasQualityTool(tools, QualityGateToolTaskValidator) {
		qualityInput := parseTaskQualityInput(task)
		qualityAssessment := taskquality.AssessTaskQuality(qualityInput)
		qualityScore = qualityAssessment.Score
		hasScore = true
		issues = append(issues, qualityAssessment.Issues...)
	}

	if hasQualityTool(tools, QualityGateToolDependencyChecker) {
		missingDependencies, dependencyIssues, err := evaluateTaskDependencies(ctx, tasks, task.Metadata["dependencies"], task.ID)
		if err != nil {
			return 0, nil, false, err
		}
		issues = append(issues, dependencyIssues...)
		if !hasScore {
			qualityScore = 100
			hasScore = true
		}
		qualityScore -= missingDependencies * 20
	}

	if !hasScore {
		return 0, nil, false, nil
	}
	if qualityScore < 0 {
		qualityScore = 0
	}
	issues = dedupeQualityIssues(issues)
	return qualityScore, issues, true, nil
}

func evaluateTaskDependencies(ctx context.Context, tasks contracts.TaskManager, rawDependencies string, taskID string) (int, []string, error) {
	dependencies := parseTaskDependencies(rawDependencies)
	if len(dependencies) == 0 {
		return 0, nil, nil
	}
	if tasks == nil {
		return 0, nil, fmt.Errorf("task manager is required for dependency quality gate")
	}

	missingDependencies := 0
	issues := []string{}
	for _, dependencyID := range dependencies {
		if strings.TrimSpace(dependencyID) == "" || dependencyID == strings.TrimSpace(taskID) {
			continue
		}
		dependency, err := tasks.GetTask(ctx, dependencyID)
		if err != nil || strings.TrimSpace(dependency.ID) == "" {
			missingDependencies++
			issues = append(issues, "dependency not resolvable: "+dependencyID)
		}
	}
	return missingDependencies, issues, nil
}

func parseTaskDependencies(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	seen := map[string]struct{}{}
	dependencies := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		dependencies = append(dependencies, part)
	}
	return dependencies
}

func resolveQualityGateTools(rawTools []string) ([]string, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}

	tools := make([]string, 0, len(rawTools))
	seen := map[string]struct{}{}
	for _, tool := range rawTools {
		tool = strings.ToLower(strings.TrimSpace(tool))
		if tool == "" {
			continue
		}
		switch tool {
		case QualityGateToolTaskValidator, QualityGateToolDependencyChecker:
		default:
			return nil, fmt.Errorf("unsupported quality gate tool %q", tool)
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools, nil
}

func hasQualityTool(tools []string, expected string) bool {
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool), expected) {
			return true
		}
	}
	return false
}

func parseTaskQualityInput(task contracts.Task) taskquality.TaskInput {
	body := stripTaskFrontmatter(task.Description)
	sections := parseQualitySections(body)
	description := strings.TrimSpace(sections["description"])
	if description == "" {
		description = strings.TrimSpace(body)
	}

	dependenciesContext := strings.TrimSpace(sections["dependencies_context"])
	if dependenciesContext == "" {
		dependenciesContext = strings.TrimSpace(task.Metadata["dependencies"])
		if dependenciesContext != "" {
			dependenciesContext = "Dependencies: " + dependenciesContext
		}
	}

	return taskquality.TaskInput{
		Title:               strings.TrimSpace(task.Title),
		Description:         description,
		AcceptanceCriteria:  strings.TrimSpace(sections["acceptance_criteria"]),
		Deliverables:        strings.TrimSpace(sections["deliverables"]),
		TestingPlan:         strings.TrimSpace(sections["testing_plan"]),
		DefinitionOfDone:    strings.TrimSpace(sections["definition_of_done"]),
		DependenciesContext: dependenciesContext,
	}
}

func parseQualitySections(raw string) map[string]string {
	sections := map[string]string{
		"description":          "",
		"acceptance_criteria":  "",
		"deliverables":         "",
		"testing_plan":         "",
		"definition_of_done":   "",
		"dependencies_context": "",
	}
	current := "description"
	for _, line := range strings.Split(raw, "\n") {
		if section, ok := parseQualitySectionHeader(line); ok {
			current = section
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sections[current] = strings.TrimSpace(sections[current] + "\n" + line)
	}
	for key, value := range sections {
		sections[key] = strings.TrimSpace(value)
	}
	return sections
}

func parseQualitySectionHeader(line string) (string, bool) {
	candidate := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(line, "#"), " "))
	candidate = strings.TrimSpace(strings.TrimSuffix(candidate, ":"))
	candidate = strings.TrimSpace(candidate)
	switch strings.ToLower(candidate) {
	case "description":
		return "description", true
	case "acceptance criteria", "acceptance":
		return "acceptance_criteria", true
	case "deliverables":
		return "deliverables", true
	case "testing plan", "testing":
		return "testing_plan", true
	case "definition of done", "definition":
		return "definition_of_done", true
	case "dependencies", "dependencies/context", "dependencies and context":
		return "dependencies_context", true
	default:
		return "", false
	}
}

func stripTaskFrontmatter(raw string) string {
	raw = strings.TrimLeft(raw, "\r\n\t ")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return strings.TrimSpace(raw)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return strings.TrimSpace(raw)
}

func dedupeQualityIssues(issues []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(issues))
	for _, issue := range issues {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			continue
		}
		if _, ok := seen[issue]; ok {
			continue
		}
		seen[issue] = struct{}{}
		unique = append(unique, issue)
	}
	return unique
}

func taskQualityScore(metadata map[string]string) (int, bool) {
	raw := strings.TrimSpace(metadata["quality_score"])
	if raw == "" {
		return 0, false
	}
	score, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return score, true
}

func taskExecutionThresholdScore(metadata map[string]string) (int, bool) {
	if score, ok := taskCoverageScore(metadata); ok {
		return score, true
	}
	if score, ok := taskQualityScore(metadata); ok {
		return score, true
	}
	return 0, false
}

func taskCoverageScore(metadata map[string]string) (int, bool) {
	raw := strings.TrimSpace(metadata["coverage"])
	if raw == "" {
		return 0, false
	}
	score, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return score, true
}

func qualityGateComment(metadata map[string]string, score int, threshold int) string {
	parts := []string{
		fmt.Sprintf("quality score %d is below threshold %d", score, threshold),
		"",
	}

	issues := qualityGateIssues(metadata)
	if len(issues) == 0 {
		return strings.Join(append(parts, "Please update the task to address these issues and rerun validation."), "\n")
	}

	formattedIssues := make([]string, 0, len(issues))
	for _, issue := range issues {
		formattedIssues = append(formattedIssues, "- "+issue)
	}
	insertAt := len(parts) - 1
	parts = append(parts[:insertAt], append([]string{"Quality issues:"}, formattedIssues...)...)
	parts = append(parts, "Please update the task to address these issues and rerun validation.")
	return strings.Join(parts, "\n")
}

func qualityGateIssues(metadata map[string]string) []string {
	raw := strings.TrimSpace(metadata["quality_issues"])
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	issues := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" {
			continue
		}
		issues = append(issues, trimmed)
	}
	return issues
}

func appendGateDecisionMetadata(metadata map[string]string, decision string, reason string) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if decision = strings.TrimSpace(decision); decision != "" {
		metadata["decision"] = decision
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		metadata["reason"] = reason
	}
	return metadata
}

func emitGateEvent(ctx context.Context, events contracts.EventSink, event contracts.Event) error {
	if events == nil {
		return nil
	}
	return events.Emit(ctx, event)
}

func markTaskBlockedWithData(deps GateDependencies, taskID string, taskData map[string]string) error {
	if deps.MarkTaskBlockedWithData == nil {
		return nil
	}
	return deps.MarkTaskBlockedWithData(taskID, taskData)
}

func clearTaskTerminalState(deps GateDependencies, taskID string) error {
	if deps.ClearTaskTerminalState == nil {
		return nil
	}
	return deps.ClearTaskTerminalState(taskID)
}

func setGateTaskStatus(ctx context.Context, deps GateDependencies, taskID string, status contracts.TaskStatus) error {
	if deps.Tasks == nil {
		return fmt.Errorf("task manager is required for quality gate status update")
	}
	return deps.Tasks.SetTaskStatus(ctx, taskID, status)
}

func setGateTaskData(ctx context.Context, deps GateDependencies, taskID string, data map[string]string) error {
	if deps.Tasks == nil {
		return fmt.Errorf("task manager is required for quality gate data update")
	}
	return deps.Tasks.SetTaskData(ctx, taskID, data)
}
