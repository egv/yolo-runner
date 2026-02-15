package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type yoloAgentConfigDefaults struct {
	Backend          string
	Model            string
	Concurrency      *int
	RunnerTimeout    *time.Duration
	WatchdogTimeout  *time.Duration
	WatchdogInterval *time.Duration
	RetryBudget      *int
}

func loadYoloAgentConfigDefaults(repoRoot string) (yoloAgentConfigDefaults, error) {
	configPath := filepath.Join(repoRoot, trackerConfigRelPath)
	model, err := loadTrackerProfilesModel(configPath)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	return resolveYoloAgentConfigDefaults(model.Agent)
}

func resolveYoloAgentConfigDefaults(model yoloAgentConfigModel) (yoloAgentConfigDefaults, error) {
	defaults := yoloAgentConfigDefaults{
		Backend: strings.TrimSpace(model.Backend),
		Model:   strings.TrimSpace(model.Model),
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
	defaults.RunnerTimeout = durationValue

	durationValue, err = parseAgentDuration("watchdog_timeout", model.WatchdogTimeout)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	defaults.WatchdogTimeout = durationValue

	durationValue, err = parseAgentDuration("watchdog_interval", model.WatchdogInterval)
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	defaults.WatchdogInterval = durationValue

	return defaults, nil
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
