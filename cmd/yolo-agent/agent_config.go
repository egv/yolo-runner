package main

import (
	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"fmt"
	"strings"
	"time"
)

type yoloAgentConfigDefaults struct {
	Backend          string
	Model            string
	Mode             string
	Concurrency      *int
	RunnerTimeout    *time.Duration
	WatchdogTimeout  *time.Duration
	WatchdogInterval *time.Duration
	RetryBudget      *int
}

func loadYoloAgentConfigDefaults(repoRoot string) (yoloAgentConfigDefaults, error) {
	catalog, err := loadCodingAgentsCatalog(repoRoot)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	model, err := newTrackerConfigService().LoadModel(repoRoot)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	return resolveYoloAgentConfigDefaults(model.Agent, catalog)
}

func resolveYoloAgentConfigDefaults(model yoloAgentConfigModel, catalog codingagents.Catalog) (yoloAgentConfigDefaults, error) {
	backend, err := normalizeAndValidateAgentBackend(model.Backend, catalog)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	mode, err := normalizeAndValidateAgentMode(model.Mode, "agent.mode")
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	configuredModel := strings.TrimSpace(model.Model)
	if configuredModel == "" {
		configuredModel = catalogBackendDefaultModel(catalog, backend)
	}
	defaults := yoloAgentConfigDefaults{
		Backend: backend,
		Model:   configuredModel,
		Mode:    mode,
	}

	if model.Concurrency != nil {
		value := *model.Concurrency
		if value <= 0 {
			return yoloAgentConfigDefaults{}, fmt.Errorf("agent.concurrency in %s must be greater than 0", trackerConfigRelPath)
		}
		defaults.Concurrency = &value
	}
	if model.RetryBudget != nil {
		value := *model.RetryBudget
		if value < 0 {
			return yoloAgentConfigDefaults{}, fmt.Errorf("agent.retry_budget in %s must be greater than or equal to 0", trackerConfigRelPath)
		}
		defaults.RetryBudget = &value
	}

	durationValue, err := parseAgentDuration("runner_timeout", model.RunnerTimeout)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	if durationValue != nil && *durationValue < 0 {
		return yoloAgentConfigDefaults{}, fmt.Errorf("agent.runner_timeout in %s must be greater than or equal to 0", trackerConfigRelPath)
	}
	defaults.RunnerTimeout = durationValue

	durationValue, err = parseAgentDuration("watchdog_timeout", model.WatchdogTimeout)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	if durationValue != nil && *durationValue <= 0 {
		return yoloAgentConfigDefaults{}, fmt.Errorf("agent.watchdog_timeout in %s must be greater than 0", trackerConfigRelPath)
	}
	defaults.WatchdogTimeout = durationValue

	durationValue, err = parseAgentDuration("watchdog_interval", model.WatchdogInterval)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	if durationValue != nil && *durationValue <= 0 {
		return yoloAgentConfigDefaults{}, fmt.Errorf("agent.watchdog_interval in %s must be greater than 0", trackerConfigRelPath)
	}
	defaults.WatchdogInterval = durationValue

	return defaults, nil
}

func catalogBackendDefaultModel(catalog codingagents.Catalog, backend string) string {
	definition, ok := catalog.Backend(backend)
	if !ok {
		return ""
	}
	return strings.TrimSpace(definition.Model)
}

func normalizeAndValidateAgentBackend(raw string, catalog codingagents.Catalog) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	normalized := normalizeBackend(value)
	if _, ok := catalog.Backend(normalized); !ok {
		backendNames := strings.Join(catalog.Names(), ", ")
		return "", fmt.Errorf("agent.backend in %s must be one of: %s", trackerConfigRelPath, backendNames)
	}
	return normalized, nil
}

func normalizeAndValidateAgentMode(raw string, field string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", nil
	}
	switch value {
	case agentModeStream, agentModeUI:
		return value, nil
	}
	return "", fmt.Errorf("%s in %s must be one of: %s, %s", field, trackerConfigRelPath, agentModeStream, agentModeUI)
}

func parseAgentDuration(field string, raw string) (*time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return nil, fmt.Errorf("agent.%s in %s must be a valid duration: %w", field, trackerConfigRelPath, err)
	}
	return &parsed, nil
}
