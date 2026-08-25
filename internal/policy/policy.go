// Package policy fetches, caches and delivers module policy. Core polls
// GateWeave for the device's policy set, caches it in the store so a
// restart offline still has the last-known documents, and notifies each
// module's Watch stream on change. Host-delivered policy is authoritative
// over anything local.
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent-core/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/retry"
)

// cacheNamespace is the store namespace policy documents cache under. It
// is core-owned; no module id collides because module ids never contain
// a dot.
const cacheNamespace = "core.policy"

// Document is the fetched policy set: one opaque payload per module.
type Document struct {
	Revision uint64                     `json:"revision"`
	Modules  map[string]json.RawMessage `json:"modules"`
}

// Manager implements hostserv.PolicyBackend and owns the fetch loop.
type Manager struct {
	Log *slog.Logger
	// URL is GateWeave's policy endpoint. Empty disables fetching: the
	// manager serves only cached/injected documents.
	URL string
	// Client for fetches; nil gets a 30s-timeout default.
	Client *http.Client
	// Interval between polls; zero gets 5 minutes.
	Interval time.Duration
	// Cache persists documents across restarts; nil disables.
	Cache hostserv.StoreBackend

	mu       sync.Mutex
	revision uint64
	docs     map[string][]byte
	watchers map[string][]chan struct{}
}

// Load primes the manager from the cache. Call once before Run.
func (m *Manager) Load() {
	if m.Cache == nil {
		return
	}
	raw, found, err := m.Cache.Get(context.Background(), cacheNamespace, "document")
	if err != nil || !found {
		return
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		m.Log.Warn("discarding corrupt policy cache", "err", err)
		return
	}
	m.apply(doc, false)
	m.Log.Info("policy loaded from cache", "revision", doc.Revision)
}

// Run polls until ctx ends. Safe to skip when URL is empty.
func (m *Manager) Run(ctx context.Context) {
	if m.URL == "" {
		return
	}
	interval := m.Interval
	if interval == 0 {
		interval = 5 * time.Minute
	}
	backoff := retry.New()
	attempt := 0
	for {
		if err := m.fetch(ctx); err != nil {
			m.Log.Warn("policy fetch failed", "err", err)
			attempt++
		} else {
			attempt = 0
		}
		wait := interval
		if attempt > 0 {
			// Back off on failure, but never poll faster than the base
			// interval even if the backoff schedule starts shorter.
			wait = max(interval, backoff.Delay(attempt))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (m *Manager) fetch(ctx context.Context) error {
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("policy endpoint returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var doc Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("malformed policy document: %w", err)
	}
	m.apply(doc, true)
	return nil
}

// apply installs a document, wakes watchers of changed modules, and
// (optionally) caches it.
func (m *Manager) apply(doc Document, cache bool) {
	m.mu.Lock()
	// Accept any revision whose content differs, not only higher ones.
	// A GateWeave restored from backup can reset revisions downward; the
	// old "refuse <= current" rule bricked delivery forever (the stale
	// revision even survived restarts via the cache). Diffing per-module
	// content below makes a reset behave like any other change.
	if doc.Revision == m.revision {
		m.mu.Unlock()
		return
	}
	if doc.Revision < m.revision {
		m.Log.Warn("policy revision regressed; treating as a server reset",
			"from", m.revision, "to", doc.Revision)
	}
	m.revision = doc.Revision
	if m.docs == nil {
		m.docs = make(map[string][]byte)
	}
	var changed []string
	seen := make(map[string]bool)
	for id, raw := range doc.Modules {
		seen[id] = true
		if string(m.docs[id]) != string(raw) {
			m.docs[id] = []byte(raw)
			changed = append(changed, id)
		}
	}
	for id := range m.docs {
		if !seen[id] {
			delete(m.docs, id)
			changed = append(changed, id)
		}
	}
	var wake []chan struct{}
	for _, id := range changed {
		wake = append(wake, m.watchers[id]...)
	}
	m.mu.Unlock()

	for _, w := range wake {
		select {
		case w <- struct{}{}:
		default:
		}
	}
	if cache && m.Cache != nil {
		if raw, err := json.Marshal(doc); err == nil {
			if err := m.Cache.Put(context.Background(), cacheNamespace, "document", raw); err != nil {
				m.Log.Warn("caching policy failed", "err", err)
			}
		}
	}
	if len(changed) > 0 {
		m.Log.Info("policy applied", "revision", doc.Revision, "changed", changed)
	}
}

// Get implements hostserv.PolicyBackend. It serves the in-memory snapshot,
// so ctx is accepted for interface parity and cancellation but never blocks.
func (m *Manager) Get(_ context.Context, module string) (uint64, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision, m.docs[module], nil
}

// Watch implements hostserv.PolicyBackend.
func (m *Manager) Watch(ctx context.Context, module string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	if m.watchers == nil {
		m.watchers = make(map[string][]chan struct{})
	}
	m.watchers[module] = append(m.watchers[module], ch)
	m.mu.Unlock()
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		ws := m.watchers[module]
		for i, w := range ws {
			if w == ch {
				m.watchers[module] = append(ws[:i], ws[i+1:]...)
				break
			}
		}
		m.mu.Unlock()
	}()
	return ch
}
