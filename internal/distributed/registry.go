package distributed

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type CapabilitySet map[Capability]struct{}

func NewCapabilitySet(values ...Capability) CapabilitySet {
	set := make(CapabilitySet, len(values))
	for _, value := range values {
		set[normalizeCapability(value)] = struct{}{}
	}
	return set
}

func (c CapabilitySet) HasAll(values ...Capability) bool {
	for _, value := range values {
		if _, ok := c[normalizeCapability(value)]; !ok {
			return false
		}
	}
	return true
}

func normalizeCapability(value Capability) Capability {
	return Capability(strings.TrimSpace(strings.ToLower(string(value))))
}

type ExecutorAdvertisement struct {
	ID                      string
	InstanceID              string
	Hostname                string
	Capabilities            CapabilitySet
	SupportedPipelines      []string
	SupportedAgents         []string
	DeclaredCapabilities    ExecutorDeclaredCapabilities
	EnvironmentProbes       ExecutorEnvironmentFeatureProbes
	CredentialFlags         map[string]bool
	ResourceHints           ExecutorResourceHints
	MaxConcurrency          int
	CurrentLoad             int
	AvailableSlots          int
	HealthStatus            string
	CapabilitySchemaVersion string
	Metadata                map[string]string
	SeenAt                  time.Time
}

type ExecutorRegistry struct {
	mu        sync.Mutex
	executors map[string]ExecutorAdvertisement
	ttl       time.Duration
	clock     func() time.Time
}

var ErrNoCapableExecutor = fmt.Errorf("no capable executors available")

func NewExecutorRegistry(ttl time.Duration, clock func() time.Time) *ExecutorRegistry {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	if clock == nil {
		clock = time.Now
	}
	return &ExecutorRegistry{
		executors: make(map[string]ExecutorAdvertisement),
		ttl:       ttl,
		clock:     func() time.Time { return clock().UTC() },
	}
}

func (r *ExecutorRegistry) now() time.Time {
	if r == nil {
		return time.Now().UTC()
	}
	return r.clock()
}

func (r *ExecutorRegistry) pruneExpired(now time.Time) {
	if r == nil {
		return
	}
	now = now.UTC()
	for id, executor := range r.executors {
		if now.Sub(executor.SeenAt) > r.ttl {
			delete(r.executors, id)
		}
	}
}

func (r *ExecutorRegistry) Register(payload ExecutorRegistrationPayload) {
	if r == nil {
		return
	}
	if err := ValidateExecutorRegistrationPayload(payload); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneExpired(now)
	id := strings.TrimSpace(payload.ExecutorID)
	if id == "" {
		return
	}
	advert := ExecutorAdvertisement{
		ID:                      id,
		InstanceID:              strings.TrimSpace(payload.InstanceID),
		Hostname:                strings.TrimSpace(payload.Hostname),
		Capabilities:            NewCapabilitySet(payload.Capabilities...),
		SupportedPipelines:      normalizeStringSlice(payload.SupportedPipelines),
		SupportedAgents:         normalizeStringSlice(payload.SupportedAgents),
		DeclaredCapabilities:    payload.DeclaredCapabilities,
		EnvironmentProbes:       payload.EnvironmentProbes,
		CredentialFlags:         copyRegistryBoolMap(payload.CredentialFlags),
		ResourceHints:           payload.ResourceHints,
		MaxConcurrency:          payload.MaxConcurrency,
		CurrentLoad:             0,
		AvailableSlots:          payload.MaxConcurrency,
		HealthStatus:            "healthy",
		CapabilitySchemaVersion: strings.TrimSpace(payload.CapabilitySchemaVersion),
		Metadata:                payload.Metadata,
		// Liveness must be based on local receipt time to avoid clock-skew false negatives.
		SeenAt: now,
	}
	if advert.CapabilitySchemaVersion == "" {
		advert.CapabilitySchemaVersion = CapabilitySchemaVersionV1
	}
	r.executors[id] = advert
}

func (r *ExecutorRegistry) Heartbeat(payload ExecutorHeartbeatPayload) {
	if r == nil {
		return
	}
	if err := ValidateExecutorHeartbeatPayload(payload); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneExpired(now)
	executorID := strings.TrimSpace(payload.ExecutorID)
	if executorID == "" {
		return
	}
	existing, ok := r.executors[executorID]
	if !ok {
		existing = ExecutorAdvertisement{
			ID:           executorID,
			Capabilities: NewCapabilitySet(),
			Metadata:     make(map[string]string),
		}
	}
	existing.SeenAt = now
	existing.InstanceID = strings.TrimSpace(payload.InstanceID)
	existing.CurrentLoad = payload.CurrentLoad
	existing.AvailableSlots = payload.AvailableSlots
	existing.MaxConcurrency = payload.MaxConcurrency
	if strings.TrimSpace(payload.HealthStatus) != "" {
		existing.HealthStatus = strings.TrimSpace(payload.HealthStatus)
	}
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]string)
	}
	for key, value := range payload.Metadata {
		existing.Metadata[key] = strings.TrimSpace(value)
	}
	r.executors[executorID] = existing
}

func (r *ExecutorRegistry) Unregister(payload ExecutorOfflinePayload) {
	if r == nil {
		return
	}
	id := strings.TrimSpace(payload.ExecutorID)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.executors, id)
}

func (r *ExecutorRegistry) IsAvailable(executorID string, now time.Time) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneExpired(now)
	_, ok := r.executors[strings.TrimSpace(executorID)]
	return ok
}

func (r *ExecutorRegistry) Pick(requirements ...Capability) (ExecutorAdvertisement, error) {
	if r == nil {
		return ExecutorAdvertisement{}, ErrNoCapableExecutor
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneExpired(now)
	required := make([]Capability, 0, len(requirements))
	for _, value := range requirements {
		required = append(required, normalizeCapability(value))
	}
	candidates := make([]ExecutorAdvertisement, 0, len(r.executors))
	for _, executor := range r.executors {
		if executor.Capabilities.HasAll(required...) {
			candidates = append(candidates, executor)
		}
	}
	if len(candidates) == 0 {
		return ExecutorAdvertisement{}, ErrNoCapableExecutor
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].SeenAt.Equal(candidates[j].SeenAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].SeenAt.After(candidates[j].SeenAt)
	})
	return candidates[0], nil
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(strings.ToLower(value))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func copyRegistryBoolMap(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		out[normalized] = value
	}
	return out
}
