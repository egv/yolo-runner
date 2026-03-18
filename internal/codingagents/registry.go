package codingagents

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
)

const (
	defaultAgentHealthTimeout        = 2 * time.Second
	defaultAgentCatalogWatchInterval = 1 * time.Second
)

type AgentHealthResult struct {
	Name    string
	Healthy bool
	Check   string
	Target  string
	Message string
	Error   error
}

type RegistryWatchEvent struct {
	Catalog Catalog
	Err     error
}

type AgentRegistry struct {
	repoRoot    string
	loadCatalog func(string) (Catalog, error)
	httpClient  *http.Client
	runCommand  func(context.Context, string, ...string) ([]byte, error)

	mu              sync.RWMutex
	catalog         Catalog
	capabilityIndex map[distributed.Capability][]string
}

func NewAgentRegistry(repoRoot string) *AgentRegistry {
	return NewAgentRegistryWithLoader(repoRoot, LoadCatalog)
}

func NewAgentRegistryWithLoader(repoRoot string, loadCatalog func(string) (Catalog, error)) *AgentRegistry {
	if loadCatalog == nil {
		loadCatalog = LoadCatalog
	}
	return &AgentRegistry{
		repoRoot:        repoRoot,
		loadCatalog:     loadCatalog,
		httpClient:      &http.Client{},
		runCommand:      runAgentHealthCommand,
		catalog:         Catalog{backends: map[string]BackendDefinition{}},
		capabilityIndex: map[distributed.Capability][]string{},
	}
}

func (r *AgentRegistry) Load() error {
	if r == nil {
		return nil
	}
	loaded, err := r.loadCatalog(r.repoRoot)
	if err != nil {
		return err
	}
	r.store(loaded)
	return nil
}

func (r *AgentRegistry) Register(definition BackendDefinition) error {
	if r == nil {
		return nil
	}
	definition = normalizeBackendDefinition(definition)
	if err := validateBackendDefinition(definition); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.catalog.add(definition); err != nil {
		return err
	}
	r.rebuildIndexLocked()
	return nil
}

func (r *AgentRegistry) Reload() error {
	return r.Load()
}

func (r *AgentRegistry) Catalog() Catalog {
	if r == nil {
		return Catalog{backends: map[string]BackendDefinition{}}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneCatalog(r.catalog)
}

func (r *AgentRegistry) Discover(required ...distributed.Capability) []BackendDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.catalog.backends == nil {
		return nil
	}
	requiredCapabilities := normalizeDistributedCapabilities(required...)
	if len(requiredCapabilities) == 0 {
		result := make([]BackendDefinition, 0, len(r.catalog.backends))
		for _, name := range r.catalog.Names() {
			definition := r.catalog.backends[name]
			result = append(result, definition)
		}
		return result
	}
	counts := map[string]int{}
	for _, capability := range requiredCapabilities {
		for _, name := range r.capabilityIndex[capability] {
			counts[name]++
		}
	}
	matching := make([]string, 0)
	for name, count := range counts {
		if count == len(requiredCapabilities) {
			matching = append(matching, name)
		}
	}
	sort.Strings(matching)
	result := make([]BackendDefinition, 0, len(matching))
	for _, name := range matching {
		definition, ok := r.catalog.backends[name]
		if !ok {
			continue
		}
		result = append(result, definition)
	}
	return result
}

func (r *AgentRegistry) HealthCheckAll(ctx context.Context) []AgentHealthResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil
	}
	r.mu.RLock()
	rc := cloneCatalog(r.catalog)
	r.mu.RUnlock()

	results := make([]AgentHealthResult, 0, len(rc.backends))
	for _, name := range rc.Names() {
		definition := rc.backends[name]
		healthConfig := definition.Health
		if healthConfig == nil || !healthConfig.Enabled {
			results = append(results, AgentHealthResult{
				Name:    name,
				Healthy: true,
				Check:   "disabled",
				Message: "health check disabled",
			})
			continue
		}
		result := AgentHealthResult{
			Name:   name,
			Check:  "",
			Target: strings.TrimSpace(healthConfig.Endpoint),
		}
		timeout, err := parseAgentHealthTimeout(healthConfig.Timeout)
		if err != nil {
			result.Healthy = false
			result.Error = err
			result.Message = err.Error()
			results = append(results, result)
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		result = r.checkDefinitionHealth(checkCtx, definition, result)
		cancel()
		results = append(results, result)
	}
	return results
}

func (r *AgentRegistry) Watch(ctx context.Context, interval time.Duration, onChange func(RegistryWatchEvent)) error {
	if r == nil {
		return nil
	}
	if onChange == nil {
		return fmt.Errorf("registry watch callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = defaultAgentCatalogWatchInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastSignature string
	for {
		if err := r.maybeReload(ctx, &lastSignature, onChange); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *AgentRegistry) maybeReload(ctx context.Context, lastSignature *string, onChange func(RegistryWatchEvent)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	signature, err := catalogSignature(r.repoRoot)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ctx.Err()
		}
		onChange(RegistryWatchEvent{Err: err})
		return nil
	}
	if *lastSignature == signature {
		return nil
	}
	loaded, loadErr := r.loadCatalog(r.repoRoot)
	if loadErr != nil {
		onChange(RegistryWatchEvent{Err: loadErr})
		*lastSignature = signature
		return nil
	}
	r.store(loaded)
	*lastSignature = signature
	onChange(RegistryWatchEvent{Catalog: cloneCatalog(loaded)})
	return nil
}

func (r *AgentRegistry) checkDefinitionHealth(ctx context.Context, definition BackendDefinition, result AgentHealthResult) AgentHealthResult {
	h := definition.Health
	if h == nil {
		result.Healthy = true
		result.Check = "disabled"
		result.Message = "health check disabled"
		return result
	}
	if strings.TrimSpace(h.Endpoint) != "" {
		result.Check = "endpoint"
		result.Target = strings.TrimSpace(h.Endpoint)
		if err := r.checkAgentHealthEndpoint(ctx, *h); err != nil {
			result.Healthy = false
			result.Error = err
			result.Message = err.Error()
			return result
		}
		result.Healthy = true
		result.Message = "ok"
		return result
	}
	if strings.TrimSpace(h.Command) != "" {
		result.Check = "command"
		result.Target = strings.TrimSpace(h.Command)
		if err := r.checkAgentHealthCommand(ctx, *h); err != nil {
			result.Healthy = false
			result.Error = err
			result.Message = err.Error()
			return result
		}
		result.Healthy = true
		result.Message = "ok"
		return result
	}
	result.Healthy = true
	result.Check = "disabled"
	result.Message = "health check target missing"
	return result
}

func (r *AgentRegistry) checkAgentHealthEndpoint(ctx context.Context, cfg BackendHealthConfig) error {
	return contracts.CheckHTTPReadiness(ctx, r.httpClient, contracts.HTTPReadinessCheck{
		Endpoint: cfg.Endpoint,
		Method:   cfg.Method,
		Headers:  cfg.Headers,
	})
}

func (r *AgentRegistry) checkAgentHealthCommand(ctx context.Context, cfg BackendHealthConfig) error {
	return contracts.CheckStdioReadiness(ctx, contracts.StdioReadinessCheck{
		Command: cfg.Command,
		Run:     r.runCommand,
	})
}

func (r *AgentRegistry) store(catalog Catalog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.catalog = cloneCatalog(catalog)
	r.rebuildIndexLocked()
}

func (r *AgentRegistry) rebuildIndexLocked() {
	if r.capabilityIndex == nil {
		r.capabilityIndex = map[distributed.Capability][]string{}
	}
	for key := range r.capabilityIndex {
		delete(r.capabilityIndex, key)
	}
	for _, name := range r.catalog.Names() {
		definition := r.catalog.backends[name]
		for _, raw := range definition.DistributedCaps {
			if normalized, ok := supportedDistributedCapability(raw); ok {
				r.capabilityIndex[normalized] = append(r.capabilityIndex[normalized], definition.Name)
			}
		}
	}
	for key, names := range r.capabilityIndex {
		sort.Strings(names)
		r.capabilityIndex[key] = names
	}
}

func normalizeDistributedCapabilities(raw ...distributed.Capability) []distributed.Capability {
	seen := map[distributed.Capability]struct{}{}
	out := make([]distributed.Capability, 0, len(raw))
	for _, value := range raw {
		capability, ok := supportedDistributedCapability(value)
		if !ok {
			continue
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func parseAgentHealthTimeout(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAgentHealthTimeout, nil
	}
	value, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid health timeout %q: %w", raw, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("health timeout must be greater than 0, got %s", value)
	}
	return value, nil
}

func runAgentHealthCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func catalogSignature(repoRoot string) (string, error) {
	cfgDir := filepath.Join(repoRoot, codingAgentConfigDir, customBackendRelPath)
	entries, err := os.ReadDir(cfgDir)
	if err != nil {
		if os.IsNotExist(err) {
			sum := sha1.Sum([]byte("missing:" + cfgDir))
			return hex.EncodeToString(sum[:]), nil
		}
		return "", fmt.Errorf("read custom agents directory %q: %w", cfgDir, err)
	}
	filenames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		switch extension {
		case ".yaml", ".yml", ".json":
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Strings(filenames)
	hash := sha1.New()
	for _, name := range filenames {
		path := filepath.Join(cfgDir, name)
		payload, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read custom agent definition %q: %w", path, err)
		}
		hash.Write([]byte(name))
		hash.Write([]byte("\n"))
		hash.Write(payload)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func cloneCatalog(catalog Catalog) Catalog {
	if catalog.backends == nil {
		return Catalog{backends: map[string]BackendDefinition{}}
	}
	cloned := Catalog{backends: map[string]BackendDefinition{}}
	for _, name := range catalog.Names() {
		if definition, ok := catalog.backends[name]; ok {
			cloned.backends[name] = definition
		}
	}
	return cloned
}
