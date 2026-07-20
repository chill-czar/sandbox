package scheduler

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/superserve-ai/sandbox/internal/db"
)

// Scheduler selects a host for a new sandbox.
type Scheduler interface {
	SelectHost(ctx context.Context, requiredCapabilities []string) (hostID string, err error)
}

const defaultCacheTTL = 30 * time.Second

// LeastLoaded picks the active host with the fewest running sandboxes
// using the "power of two random choices" algorithm. Instead of always
// picking the globally least-loaded host (which causes thundering herd
// when many creates arrive simultaneously), it samples two random hosts
// from the active set and picks the one with fewer sandboxes.
//
// With one host this degenerates to always picking that host. With two
// or more it spreads load naturally without coordination. The algorithm
// is proven to reduce max load from O(log n / log log n) to O(log log n).
//
// If no host rows exist in the table, SelectHost falls back to
// DefaultHostID so sandbox creation works without populating the host table.
type LeastLoaded struct {
	DB            *db.Queries
	DefaultHostID string        // fallback when no host rows exist
	TTL           time.Duration // 0 = use defaultCacheTTL

	mu    sync.RWMutex
	cache map[string]hostCacheEntry
}

type hostCacheEntry struct {
	hosts    []db.ListActiveHostsByLoadRow
	cachedAt time.Time
}

func (s *LeastLoaded) ttl() time.Duration {
	if s.TTL > 0 {
		return s.TTL
	}
	return defaultCacheTTL
}

func (s *LeastLoaded) SelectHost(ctx context.Context, requiredCapabilities []string) (string, error) {
	hosts, err := s.loadHosts(ctx, requiredCapabilities)
	if err != nil {
		return "", err
	}
	if len(hosts) == 0 {
		if s.DefaultHostID != "" {
			return s.DefaultHostID, nil
		}
		return "", fmt.Errorf("no active hosts available")
	}
	if len(hosts) == 1 {
		return hosts[0].ID, nil
	}

	// Power of two random choices: pick two random hosts, return the
	// one with fewer active sandboxes. This avoids the thundering-herd
	// problem where every concurrent create picks the same least-loaded
	// host from a globally-sorted list.
	a := rand.IntN(len(hosts))
	b := rand.IntN(len(hosts) - 1)
	if b >= a {
		b++ // ensures b != a
	}
	if hosts[a].ActiveSandboxCount <= hosts[b].ActiveSandboxCount {
		return hosts[a].ID, nil
	}
	return hosts[b].ID, nil
}

func (s *LeastLoaded) loadHosts(ctx context.Context, requiredCapabilities []string) ([]db.ListActiveHostsByLoadRow, error) {
	key, normalized := capabilityCacheKey(requiredCapabilities)
	s.mu.RLock()
	if entry, ok := s.cache[key]; ok && time.Since(entry.cachedAt) < s.ttl() {
		s.mu.RUnlock()
		return entry.hosts, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.cache[key]; ok && time.Since(entry.cachedAt) < s.ttl() {
		return entry.hosts, nil
	}

	hosts, err := s.DB.ListActiveHostsByLoad(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("list active hosts by load: %w", err)
	}
	if s.cache == nil {
		s.cache = make(map[string]hostCacheEntry)
	}
	s.cache[key] = hostCacheEntry{hosts: hosts, cachedAt: time.Now()}
	return hosts, nil
}

func capabilityCacheKey(capabilities []string) (string, []string) {
	set := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability != "" {
			set[capability] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(set))
	for capability := range set {
		normalized = append(normalized, capability)
	}
	sort.Strings(normalized)
	return strings.Join(normalized, "\x00"), normalized
}

// Invalidate drops the cached host list so the next SelectHost reflects
// changes immediately.
func (s *LeastLoaded) Invalidate() {
	s.mu.Lock()
	s.cache = nil
	s.mu.Unlock()
}
