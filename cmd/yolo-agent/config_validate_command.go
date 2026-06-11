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

	if err := validateExplicitArcReviewWatchConfigValues(*repo); err != nil {
		return reportInvalidConfig(err, format)
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
	if err := validateArcReviewWatchConfigDefaults(*repo, model.ArcReviewWatch); err != nil {
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

func validateArcReviewWatchConfigDefaults(repoRoot string, model arcReviewWatchConfigModel) error {
	if err := validateExplicitArcReviewWatchConfigValues(repoRoot); err != nil {
		return err
	}

	cfg, err := resolveArcReviewWatchConfig(model, repoRoot)
	if err != nil {
		return err
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("arc_review_watch.poll_interval in %s must be greater than 0", trackerConfigRelPath)
	}
	if strings.TrimSpace(cfg.LockPath) == "" {
		return fmt.Errorf("arc_review_watch.lock_path in %s must not be empty", trackerConfigRelPath)
	}
	if strings.TrimSpace(cfg.StatePath) == "" {
		return fmt.Errorf("arc_review_watch.state_path in %s must not be empty", trackerConfigRelPath)
	}
	if cfg.MaxConcurrency <= 0 {
		return fmt.Errorf("arc_review_watch.max_concurrency in %s must be greater than 0", trackerConfigRelPath)
	}
	return nil
}

func validateExplicitArcReviewWatchConfigValues(repoRoot string) error {
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

	watchNode := configValidationYAMLMappingValue(root, "arc_review_watch")
	if watchNode == nil || configValidationYAMLIsNull(watchNode) {
		return nil
	}
	if watchNode.Kind != yaml.MappingNode {
		return fmt.Errorf("arc_review_watch in %s must be a mapping", trackerConfigRelPath)
	}

	for _, field := range []string{"poll_interval", "lock_path", "state_path", "reviewer"} {
		if node := configValidationYAMLMappingValue(watchNode, field); node != nil && strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("arc_review_watch.%s in %s must not be empty", field, trackerConfigRelPath)
		}
	}

	if node := configValidationYAMLMappingValue(watchNode, "max_concurrency"); node != nil && strings.TrimSpace(node.Value) == "" {
		return fmt.Errorf("arc_review_watch.max_concurrency in %s must not be empty", trackerConfigRelPath)
	} else if node != nil && node.Tag != "!!int" {
		return fmt.Errorf("arc_review_watch.max_concurrency in %s must be an integer", trackerConfigRelPath)
	}
	if node := configValidationYAMLMappingValue(watchNode, "allow_ship"); node != nil && strings.TrimSpace(node.Value) == "" {
		return fmt.Errorf("arc_review_watch.allow_ship in %s must not be empty", trackerConfigRelPath)
	} else if node != nil && node.Tag != "!!bool" {
		return fmt.Errorf("arc_review_watch.allow_ship in %s must be true or false", trackerConfigRelPath)
	}

	for _, field := range []string{"workspaces", "branches"} {
		node := configValidationYAMLMappingValue(watchNode, field)
		if node == nil || configValidationYAMLIsNull(node) {
			continue
		}
		if node.Kind != yaml.SequenceNode {
			return fmt.Errorf("arc_review_watch.%s in %s must be a list", field, trackerConfigRelPath)
		}
		for i, item := range node.Content {
			if strings.TrimSpace(item.Value) == "" {
				return fmt.Errorf("arc_review_watch.%s[%d] in %s must not be empty", field, i, trackerConfigRelPath)
			}
		}
	}

	arcMountNode := configValidationYAMLMappingValue(watchNode, "arc_mount")
	if arcMountNode == nil || configValidationYAMLIsNull(arcMountNode) {
		return nil
	}
	if arcMountNode.Kind != yaml.MappingNode {
		return fmt.Errorf("arc_review_watch.arc_mount in %s must be a mapping", trackerConfigRelPath)
	}
	for _, field := range []string{"mount", "store", "object_store"} {
		if node := configValidationYAMLMappingValue(arcMountNode, field); node != nil && strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("arc_review_watch.arc_mount.%s in %s must not be empty", field, trackerConfigRelPath)
		}
	}
	for _, field := range []string{"enabled", "allow_other", "ssh_tokens", "no_hardlinks", "no_auto_rehash"} {
		if node := configValidationYAMLMappingValue(arcMountNode, field); node != nil && strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("arc_review_watch.arc_mount.%s in %s must not be empty", field, trackerConfigRelPath)
		} else if node != nil && node.Tag != "!!bool" {
			return fmt.Errorf("arc_review_watch.arc_mount.%s in %s must be true or false", field, trackerConfigRelPath)
		}
	}
	for _, field := range []string{"inode_cache_size", "cache_size", "override_lazy_checkout"} {
		if node := configValidationYAMLMappingValue(arcMountNode, field); node != nil && strings.TrimSpace(node.Value) == "" {
			return fmt.Errorf("arc_review_watch.arc_mount.%s in %s must not be empty", field, trackerConfigRelPath)
		} else if node != nil && node.Tag != "!!int" {
			return fmt.Errorf("arc_review_watch.arc_mount.%s in %s must be an integer", field, trackerConfigRelPath)
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
		"tracker_agent.status_transitions.ready",
		"tracker_agent.status_transitions.in_progress",
		"tracker_agent.status_transitions.completed",
		"tracker_agent.status_transitions.blocked",
		"tracker_agent.status_transitions.failed",
		"tracker_agent.status_transitions.completed_resolution",
		"arc_review_watch.poll_interval",
		"arc_review_watch.lock_path",
		"arc_review_watch.state_path",
		"arc_review_watch.reviewer",
		"arc_review_watch.max_concurrency",
		"arc_review_watch.allow_ship",
		"arc_review_watch.workspaces",
		"arc_review_watch.branches",
		"arc_review_watch.arc_mount.enabled",
		"arc_review_watch.arc_mount.mount",
		"arc_review_watch.arc_mount.store",
		"arc_review_watch.arc_mount.object_store",
		"arc_review_watch.arc_mount.allow_other",
		"arc_review_watch.arc_mount.ssh_tokens",
		"arc_review_watch.arc_mount.no_hardlinks",
		"arc_review_watch.arc_mount.no_auto_rehash",
		"arc_review_watch.arc_mount.inode_cache_size",
		"arc_review_watch.arc_mount.cache_size",
		"arc_review_watch.arc_mount.override_lazy_checkout",
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
	indexedPrefix := regexp.MustCompile("^" + regexp.QuoteMeta(field) + `\[[0-9]+\] in ` + regexp.QuoteMeta(trackerConfigRelPath) + `\s+`)
	reason = indexedPrefix.ReplaceAllString(reason, "")
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
	case "tracker_agent.status_transitions.ready":
		return "Set tracker_agent.status_transitions.ready to a Tracker transition id, set it to an empty string to disable it, or omit it to use the default."
	case "tracker_agent.status_transitions.in_progress":
		return "Set tracker_agent.status_transitions.in_progress to a Tracker transition id, set it to an empty string to disable it, or omit it to use the default."
	case "tracker_agent.status_transitions.completed":
		return "Set tracker_agent.status_transitions.completed to a Tracker transition id, set it to an empty string to disable it, or omit it to use the default."
	case "tracker_agent.status_transitions.blocked":
		return "Set tracker_agent.status_transitions.blocked to a Tracker transition id, set it to an empty string to disable it, or omit it to use the default."
	case "tracker_agent.status_transitions.failed":
		return "Set tracker_agent.status_transitions.failed to a Tracker transition id, set it to an empty string to disable it, or omit it to use the default."
	case "tracker_agent.status_transitions.completed_resolution":
		return "Set tracker_agent.status_transitions.completed_resolution to a Tracker resolution key, set it to an empty string to omit resolution, or omit it to use the default."
	case "arc_review_watch.poll_interval":
		return "Set arc_review_watch.poll_interval to a valid duration greater than 0, or omit it to use the default."
	case "arc_review_watch.lock_path":
		return "Set arc_review_watch.lock_path to a non-empty file path, or omit it to use the default."
	case "arc_review_watch.state_path":
		return "Set arc_review_watch.state_path to a non-empty file path, or omit it to use the default."
	case "arc_review_watch.reviewer":
		return "Set arc_review_watch.reviewer to a non-empty reviewer login, or omit it to leave reviewer filtering unset."
	case "arc_review_watch.max_concurrency":
		return "Set arc_review_watch.max_concurrency to an integer greater than 0, or omit it to use the default."
	case "arc_review_watch.allow_ship":
		return "Set arc_review_watch.allow_ship to true or false, or omit it to keep shipping disabled."
	case "arc_review_watch.workspaces":
		return "Set arc_review_watch.workspaces to a list of non-empty workspace paths, or omit it to watch all configured workspaces."
	case "arc_review_watch.branches":
		return "Set arc_review_watch.branches to a list of non-empty branch names, or omit it to watch all configured branches."
	case "arc_review_watch.arc_mount.enabled":
		return "Set arc_review_watch.arc_mount.enabled to true or false, or omit it to keep arc mounting disabled."
	case "arc_review_watch.arc_mount.mount":
		return "Set arc_review_watch.arc_mount.mount to a non-empty mount path, or omit it to use the default."
	case "arc_review_watch.arc_mount.store":
		return "Set arc_review_watch.arc_mount.store to a non-empty store path, or omit it to use the default."
	case "arc_review_watch.arc_mount.object_store":
		return "Set arc_review_watch.arc_mount.object_store to a non-empty object store path, or omit it to use the default."
	case "arc_review_watch.arc_mount.allow_other":
		return "Set arc_review_watch.arc_mount.allow_other to true or false, or omit it to use the default."
	case "arc_review_watch.arc_mount.ssh_tokens":
		return "Set arc_review_watch.arc_mount.ssh_tokens to true or false, or omit it to use the default."
	case "arc_review_watch.arc_mount.no_hardlinks":
		return "Set arc_review_watch.arc_mount.no_hardlinks to true or false, or omit it to use the default."
	case "arc_review_watch.arc_mount.no_auto_rehash":
		return "Set arc_review_watch.arc_mount.no_auto_rehash to true or false, or omit it to use the default."
	case "arc_review_watch.arc_mount.inode_cache_size":
		return "Set arc_review_watch.arc_mount.inode_cache_size to an integer greater than 0, or omit it to use the default."
	case "arc_review_watch.arc_mount.cache_size":
		return "Set arc_review_watch.arc_mount.cache_size to an integer greater than 0, or omit it to use the default."
	case "arc_review_watch.arc_mount.override_lazy_checkout":
		return "Set arc_review_watch.arc_mount.override_lazy_checkout to an integer greater than or equal to 0, or omit it to use the default."
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
