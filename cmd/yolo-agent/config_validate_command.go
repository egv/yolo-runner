package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const configValidateSchemaVersion = "v1"

var configFieldPattern = regexp.MustCompile(`[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+`)

type configValidateOutputFormat string

const (
	configValidateOutputFormatText configValidateOutputFormat = "text"
	configValidateOutputFormatJSON configValidateOutputFormat = "json"
)

type configValidateDiagnostic struct {
	Field       string `json:"field"`
	Reason      string `json:"reason"`
	Remediation string `json:"remediation"`
}

type configValidateResultPayload struct {
	SchemaVersion string                     `json:"schema_version"`
	Status        string                     `json:"status"`
	Diagnostics   []configValidateDiagnostic `json:"diagnostics"`
}

func defaultRunConfigValidateCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent-config-validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent config validate [flags]")
	}

	repo := fs.String("repo", ".", "Repository root")
	profile := fs.String("profile", "", "Tracker profile name from .yolo-runner/config.yaml")
	root := fs.String("root", "", "Root task ID for scope validation")
	outputFormat := fs.String("format", string(configValidateOutputFormatText), "Output format: text|json")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments for config validate: %s\n", strings.Join(fs.Args(), " "))
		return 1
	}
	format, err := parseConfigValidateOutputFormat(*outputFormat)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	service := newTrackerConfigService()
	model, err := service.LoadModel(*repo)
	if err != nil {
		return reportInvalidConfig(err, format)
	}
	catalog, err := loadCodingAgentsCatalog(*repo)
	if err != nil {
		return reportInvalidConfig(err, format)
	}
	if _, err := resolveYoloAgentConfigDefaults(model.Agent, catalog); err != nil {
		return reportInvalidConfig(err, format)
	}
	if err := validateTrackerAgentConfigDefaults(*repo, model.TrackerAgent); err != nil {
		return reportInvalidConfig(err, format)
	}

	profileName := resolveProfileSelectionPolicy(profileSelectionInput{
		FlagValue: *profile,
		EnvValue:  os.Getenv("YOLO_PROFILE"),
	})
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = strings.TrimSpace(model.DefaultProfile)
	}
	if profileName == "" {
		profileName = defaultProfileName
	}

	profileDef, ok := model.Profiles[profileName]
	if !ok {
		return reportInvalidConfig(fmt.Errorf("tracker profile %q not found (available: %s)", profileName, strings.Join(sortedProfileNames(model.Profiles), ", ")), format)
	}

	rootID := strings.TrimSpace(*root)
	if rootID == "" && profileDef.Tracker.TK != nil {
		scopeRoot := strings.TrimSpace(profileDef.Tracker.TK.Scope.Root)
		if scopeRoot != "" {
			rootID = scopeRoot
		}
	}

	if _, err := validateTrackerModel(profileName, profileDef.Tracker, rootID, os.Getenv); err != nil {
		return reportInvalidConfig(err, format)
	}

	if format == configValidateOutputFormatJSON {
		emitConfigValidateJSON(configValidateResultPayload{
			SchemaVersion: configValidateSchemaVersion,
			Status:        "valid",
			Diagnostics:   []configValidateDiagnostic{},
		})
		return 0
	}

	fmt.Fprintln(os.Stdout, "config is valid")
	return 0
}

func validateTrackerAgentConfigDefaults(repoRoot string, model trackerAgentConfigModel) error {
	if err := validateExplicitTrackerAgentConfigValues(repoRoot); err != nil {
		return err
	}

	cfg, err := resolveTrackerAgentConfig(model, repoRoot)
	if err != nil {
		return err
	}
	return validateResolvedTrackerAgentConfigDefaults(cfg)
}

func validateResolvedTrackerAgentConfigDefaults(cfg trackerAgentConfig) error {
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("tracker_agent.poll_interval in %s must be greater than 0", trackerConfigRelPath)
	}
	if strings.TrimSpace(cfg.LockPath) == "" {
		return fmt.Errorf("tracker_agent.lock_path in %s must not be empty", trackerConfigRelPath)
	}

	labels := []struct {
		field string
		value string
	}{
		{field: "tracker_agent.labels.ready", value: cfg.Labels.Ready},
		{field: "tracker_agent.labels.in_progress", value: cfg.Labels.InProgress},
		{field: "tracker_agent.labels.completed", value: cfg.Labels.Completed},
		{field: "tracker_agent.labels.blocked", value: cfg.Labels.Blocked},
		{field: "tracker_agent.labels.failed", value: cfg.Labels.Failed},
	}
	for _, label := range labels {
		if strings.TrimSpace(label.value) == "" {
			return fmt.Errorf("%s in %s must not be empty", label.field, trackerConfigRelPath)
		}
	}
	return nil
}

func validateExplicitTrackerAgentConfigValues(repoRoot string) error {
	content, err := os.ReadFile(filepath.Join(repoRoot, trackerConfigRelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read config file at %s: %w", trackerConfigRelPath, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("cannot parse config file at %s: %w", trackerConfigRelPath, err)
	}
	root := configValidationYAMLDocumentRoot(&doc)
	if root == nil {
		return nil
	}

	trackerAgentNode := configValidationYAMLMappingValue(root, "tracker_agent")
	if trackerAgentNode == nil || configValidationYAMLIsNull(trackerAgentNode) {
		return nil
	}
	if trackerAgentNode.Kind != yaml.MappingNode {
		return fmt.Errorf("tracker_agent in %s must be a mapping", trackerConfigRelPath)
	}

	if node := configValidationYAMLMappingValue(trackerAgentNode, "poll_interval"); node != nil && strings.TrimSpace(node.Value) == "" {
		return fmt.Errorf("tracker_agent.poll_interval in %s must not be empty", trackerConfigRelPath)
	}
	if node := configValidationYAMLMappingValue(trackerAgentNode, "lock_path"); node != nil && strings.TrimSpace(node.Value) == "" {
		return fmt.Errorf("tracker_agent.lock_path in %s must not be empty", trackerConfigRelPath)
	}

	labelsNode := configValidationYAMLMappingValue(trackerAgentNode, "labels")
	if labelsNode == nil || configValidationYAMLIsNull(labelsNode) {
		return nil
	}
	if labelsNode.Kind != yaml.MappingNode {
		return fmt.Errorf("tracker_agent.labels in %s must be a mapping", trackerConfigRelPath)
	}

	for _, labelField := range []string{"ready", "in_progress", "completed", "blocked", "failed"} {
		if node := configValidationYAMLMappingValue(labelsNode, labelField); node != nil && strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("tracker_agent.labels.%s in %s must not be empty", labelField, trackerConfigRelPath)
		}
	}
	return nil
}

func configValidationYAMLDocumentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

func configValidationYAMLMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func configValidationYAMLIsNull(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func parseConfigValidateOutputFormat(raw string) (configValidateOutputFormat, error) {
	switch configValidateOutputFormat(strings.ToLower(strings.TrimSpace(raw))) {
	case configValidateOutputFormatText:
		return configValidateOutputFormatText, nil
	case configValidateOutputFormatJSON:
		return configValidateOutputFormatJSON, nil
	default:
		return "", fmt.Errorf("unsupported --format value %q (supported: text, json)", raw)
	}
}

func reportInvalidConfig(err error, format configValidateOutputFormat) int {
	diagnostic := classifyConfigValidationError(err)
	if format == configValidateOutputFormatJSON {
		emitConfigValidateJSON(configValidateResultPayload{
			SchemaVersion: configValidateSchemaVersion,
			Status:        "invalid",
			Diagnostics:   []configValidateDiagnostic{diagnostic},
		})
		return 1
	}

	fmt.Fprintln(os.Stderr, "config is invalid")
	fmt.Fprintf(os.Stderr, "field: %s\n", diagnostic.Field)
	fmt.Fprintf(os.Stderr, "reason: %s\n", diagnostic.Reason)
	fmt.Fprintf(os.Stderr, "remediation: %s\n", diagnostic.Remediation)
	return 1
}

func emitConfigValidateJSON(payload configValidateResultPayload) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}

func classifyConfigValidationError(err error) configValidateDiagnostic {
	message := strings.TrimSpace(err.Error())
	field := inferConfigField(message)
	reason := inferConfigReason(message, field)
	remediation := inferConfigRemediation(field, message)
	return configValidateDiagnostic{
		Field:       field,
		Reason:      reason,
		Remediation: remediation,
	}
}

func inferConfigField(message string) string {
	knownFields := []string{
		"agent.backend",
		"agent.concurrency",
		"agent.retry_budget",
		"agent.runner_timeout",
		"agent.watchdog_timeout",
		"agent.watchdog_interval",
		"tracker_agent.poll_interval",
		"tracker_agent.lock_path",
		"tracker_agent.labels.ready",
		"tracker_agent.labels.in_progress",
		"tracker_agent.labels.completed",
		"tracker_agent.labels.blocked",
		"tracker_agent.labels.failed",
		"tracker.type",
		"linear.scope.workspace",
		linearTokenEnvVarLabel,
		"github.scope.owner",
		"github.scope.repo",
		githubTokenEnvVarLabel,
	}
	for _, field := range knownFields {
		if strings.Contains(message, field) {
			return field
		}
	}
	if strings.Contains(message, "missing auth token from") {
		if strings.Contains(message, "<linear-api-token>") {
			return linearTokenEnvVarLabel
		}
		if strings.Contains(message, "<github-personal-access-token>") {
			return githubTokenEnvVarLabel
		}
		return "auth.token_env"
	}
	if strings.Contains(message, "tracker profile") && strings.Contains(message, "not found") {
		return "default_profile"
	}
	if strings.Contains(message, "unsupported tracker type") {
		return "tracker.type"
	}
	if strings.Contains(message, "cannot parse config file") || strings.Contains(message, "cannot read config file") {
		return "config.file"
	}

	if match := configFieldPattern.FindString(message); match != "" {
		return match
	}
	return "config"
}

func inferTokenEnvFromMessage(message string) string {
	const prefix = "missing auth token from "
	idx := strings.Index(message, prefix)
	if idx == -1 {
		return ""
	}
	rest := message[idx+len(prefix):]
	for i, r := range rest {
		if r == ' ' || r == '\t' || r == '\n' {
			return strings.TrimSpace(rest[:i])
		}
	}
	return strings.TrimSpace(rest)
}

func inferConfigReason(message string, field string) string {
	reason := message
	prefixes := []string{
		field + " in " + trackerConfigRelPath + " ",
		field + " ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(reason, prefix) {
			reason = strings.TrimSpace(strings.TrimPrefix(reason, prefix))
		}
	}
	if strings.HasPrefix(reason, "is ") {
		reason = strings.TrimSpace(strings.TrimPrefix(reason, "is "))
	}
	if idx := strings.Index(reason, ";"); idx >= 0 {
		reason = strings.TrimSpace(reason[:idx])
	}
	return reason
}

func inferConfigRemediation(field string, message string) string {
	switch field {
	case "agent.backend":
		return "Set agent.backend to a configured coding backend in .yolo-runner/config.yaml."
	case "agent.concurrency":
		return "Set agent.concurrency to an integer greater than 0 in .yolo-runner/config.yaml."
	case "agent.retry_budget":
		return "Set agent.retry_budget to an integer greater than or equal to 0 in .yolo-runner/config.yaml."
	case "agent.runner_timeout":
		return "Set agent.runner_timeout to a valid duration (for example 30s or 5m) in .yolo-runner/config.yaml."
	case "agent.watchdog_timeout":
		return "Set agent.watchdog_timeout to a valid duration greater than 0 in .yolo-runner/config.yaml."
	case "agent.watchdog_interval":
		return "Set agent.watchdog_interval to a valid duration greater than 0 in .yolo-runner/config.yaml."
	case "tracker_agent.poll_interval":
		return "Set tracker_agent.poll_interval to a valid duration greater than 0, or omit it to use the default."
	case "tracker_agent.lock_path":
		return "Set tracker_agent.lock_path to a non-empty file path, or omit it to use the default."
	case "tracker_agent.labels.ready":
		return "Set tracker_agent.labels.ready to a non-empty label name, or omit it to use the default."
	case "tracker_agent.labels.in_progress":
		return "Set tracker_agent.labels.in_progress to a non-empty label name, or omit it to use the default."
	case "tracker_agent.labels.completed":
		return "Set tracker_agent.labels.completed to a non-empty label name, or omit it to use the default."
	case "tracker_agent.labels.blocked":
		return "Set tracker_agent.labels.blocked to a non-empty label name, or omit it to use the default."
	case "tracker_agent.labels.failed":
		return "Set tracker_agent.labels.failed to a non-empty label name, or omit it to use the default."
	case "tracker.type":
		return "Set tracker.type to a supported tracker (tk, linear, github) in .yolo-runner/config.yaml."
	case "linear.scope.workspace":
		return "Set linear.scope.workspace to exactly one workspace slug in .yolo-runner/config.yaml."
	case linearTokenEnvVarLabel:
		return "Set linear.auth.token_env to an env var name and export that variable with your Linear API token."
	case "github.scope.owner":
		return "Set github.scope.owner to a single GitHub organization or username in .yolo-runner/config.yaml."
	case "github.scope.repo":
		return "Set github.scope.repo to a single repository name (without owner) in .yolo-runner/config.yaml."
	case githubTokenEnvVarLabel:
		return "Set github.auth.token_env to an env var name and export that variable with your GitHub personal access token."
	case "default_profile":
		return "Set default_profile to an existing entry under profiles, or pass --profile with a valid profile name."
	case "config.file":
		return "Fix .yolo-runner/config.yaml syntax and keys, then rerun yolo-agent config validate."
	default:
		if strings.Contains(message, "missing auth token from ") {
			tokenEnv := inferTokenEnvFromMessage(message)
			if tokenEnv != "" {
				return fmt.Sprintf("Export %s in your shell with the configured API token, then rerun validation.", tokenEnv)
			}
		}
		return "Update .yolo-runner/config.yaml to satisfy the reported constraint, then rerun yolo-agent config validate."
	}
}
