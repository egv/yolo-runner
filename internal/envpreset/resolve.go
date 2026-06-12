package envpreset

import (
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/codingagents"
)

const (
	defaultAgentBackend     = "opencode"
	defaultWatchdogTimeout  = 10 * time.Minute
	defaultWatchdogInterval = 5 * time.Second
	legacyGitLandingType    = "git"
)

type ResolvedAgent struct {
	Backend          string
	Model            string
	RunnerTimeout    time.Duration
	WatchdogTimeout  time.Duration
	WatchdogInterval time.Duration
}

func ResolveAgent(preset Preset) (ResolvedAgent, error) {
	catalog, err := codingagents.LoadCatalog("")
	if err != nil {
		return ResolvedAgent{}, err
	}
	return resolveAgent(preset.Agent, catalog)
}

func ResolveLanding(preset Preset) (LandingType, error) {
	value := LandingType(strings.ToLower(strings.TrimSpace(string(preset.Landing.Type))))
	switch value {
	case "", LandingTypeGitMerge:
		return LandingTypeGitMerge, nil
	case LandingTypeArcPR, LandingTypeNone:
		return value, nil
	case legacyGitLandingType:
		return LandingTypeGitMerge, nil
	default:
		return "", fmt.Errorf("preset landing.type must be one of: %s, %s, %s", LandingTypeGitMerge, LandingTypeArcPR, LandingTypeNone)
	}
}

func resolveAgent(agent Agent, catalog codingagents.Catalog) (ResolvedAgent, error) {
	backend := strings.ToLower(strings.TrimSpace(agent.Backend))
	if backend == "" {
		backend = defaultAgentBackend
	}

	definition, ok := catalog.Backend(backend)
	if !ok {
		return ResolvedAgent{}, fmt.Errorf("preset agent.backend must be one of: %s", strings.Join(catalog.Names(), ", "))
	}

	model := strings.TrimSpace(agent.Model)
	if model == "" {
		model = strings.TrimSpace(definition.Model)
	}

	runnerTimeout := agent.RunnerTimeout
	if runnerTimeout < 0 {
		return ResolvedAgent{}, fmt.Errorf("preset agent.runner_timeout must be greater than or equal to 0")
	}

	watchdogTimeout := agent.WatchdogTimeout
	if watchdogTimeout < 0 {
		return ResolvedAgent{}, fmt.Errorf("preset agent.watchdog_timeout must be greater than 0")
	}
	if watchdogTimeout == 0 {
		watchdogTimeout = defaultWatchdogTimeout
	}

	watchdogInterval := agent.WatchdogInterval
	if watchdogInterval < 0 {
		return ResolvedAgent{}, fmt.Errorf("preset agent.watchdog_interval must be greater than 0")
	}
	if watchdogInterval == 0 {
		watchdogInterval = defaultWatchdogInterval
	}

	return ResolvedAgent{
		Backend:          backend,
		Model:            model,
		RunnerTimeout:    runnerTimeout,
		WatchdogTimeout:  watchdogTimeout,
		WatchdogInterval: watchdogInterval,
	}, nil
}
