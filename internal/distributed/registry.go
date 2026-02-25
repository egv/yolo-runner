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
	ID           string
	Capabilities CapabilitySet
	Metadata     map[string]string
	SeenAt       time.Time
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
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneExpired(now)
	id := strings.TrimSpace(payload.ExecutorID)
	if id == "" {
		return
	}
	advert := ExecutorAdvertisement{
		ID:           id,
		Capabilities: NewCapabilitySet(payload.Capabilities...),
		Metadata:     payload.Metadata,
		// Liveness must be based on local receipt time to avoid clock-skew false negatives.
		SeenAt: now,
	}
	r.executors[id] = advert
}

func (r *ExecutorRegistry) Heartbeat(payload ExecutorHeartbeatPayload) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneExpired(now)
	existing, ok := r.executors[payload.ExecutorID]
	if !ok {
		existing = ExecutorAdvertisement{
			ID:           payload.ExecutorID,
			Capabilities: NewCapabilitySet(),
			Metadata:     make(map[string]string),
		}
	}
	existing.SeenAt = now
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]string)
	}
	for key, value := range payload.Metadata {
		existing.Metadata[key] = strings.TrimSpace(value)
	}
	r.executors[payload.ExecutorID] = existing
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
