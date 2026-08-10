// Package sysinfo implements the sysinfo module: periodic host inventory
// published to the event bus and heartbeats to the platform. It exercises
// every Host surface exactly once, which is why it doubles as the module
// template.
package sysinfo

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/deploymenttheory/weaveplatform-sdk/config"
	"github.com/deploymenttheory/weaveplatform-sdk/modulesdk"
)

// Config is the module's handshake-delivered configuration.
type Config struct {
	// IntervalSeconds between collections. Policy key "interval_seconds"
	// overrides at runtime; policy wins over config, config over default.
	IntervalSeconds int `json:"interval_seconds"`
}

// Module implements modulesdk.Module.
type Module struct {
	host      modulesdk.Host
	cfg       Config
	stopWatch context.CancelFunc

	mu            sync.Mutex
	interval      time.Duration
	lastCollected time.Time
	lastErr       error
}

// New returns the module with defaults.
func New() *Module {
	return &Module{cfg: Config{IntervalSeconds: 900}}
}

// ID implements modulesdk.Module.
func (m *Module) ID() string { return "sysinfo" }

// Requires implements modulesdk.Module.
func (m *Module) Requires() []modulesdk.Capability {
	return []modulesdk.Capability{"platform.osinfo"}
}

// Init implements modulesdk.Module: read config, apply policy, declare the
// status surface, and watch for policy changes.
func (m *Module) Init(ctx context.Context, host modulesdk.Host) error {
	m.host = host

	// Config document (delivered by core) over defaults.
	// (Config arrives via the runtime; modulesdk exposes it through the
	// WEAVE_CONFIG seam — the runtime hands it to us at Init.)
	m.interval = time.Duration(m.cfg.IntervalSeconds) * time.Second

	// Policy over config: read once now, then watch. The watch must
	// outlive Init — its context is the module's lifetime, not the Init
	// RPC's (which is cancelled the moment Init returns).
	if doc, err := host.Policy().Get(ctx); err == nil {
		m.applyPolicy(doc)
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	m.stopWatch = cancel
	watch, err := host.Policy().Watch(watchCtx)
	if err == nil {
		go func() {
			for doc := range watch {
				m.applyPolicy(doc)
			}
		}()
	}

	if err := host.UI().Declare(modulesdk.Surface{
		ID:    "inventory",
		Title: "Host inventory",
		Kind:  "card",
	}); err != nil {
		return err
	}

	// The scheduled collection: runs once at Start, then on each interval.
	// EveryFunc re-reads the interval each cycle so a policy change to
	// sysinfo/interval actually changes the collection cadence, not just
	// the reported value.
	host.Schedule(modulesdk.Job{
		Name:      "collect",
		EveryFunc: m.currentInterval,
		Run:       m.collectAndReport,
	})
	return nil
}

// SetConfig is called by the runtime before Init with the raw config doc.
func (m *Module) SetConfig(doc []byte) error {
	return config.Load(doc, &m.cfg)
}

// Start implements modulesdk.Module. Jobs start with the runtime; nothing
// else to do.
func (m *Module) Start(ctx context.Context) error { return nil }

// Stop implements modulesdk.Module.
func (m *Module) Stop(ctx context.Context) error {
	if m.stopWatch != nil {
		m.stopWatch()
	}
	return nil
}

// Health implements modulesdk.Module: healthy while collections succeed
// within 2× the interval; degraded (not unhealthy) when they stale — a
// stale inventory is not worth a restart.
func (m *Module) Health() modulesdk.Health {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastCollected.IsZero() {
		return modulesdk.Health{Status: modulesdk.HealthHealthy, Reason: "no collection yet"}
	}
	if m.lastErr != nil {
		return modulesdk.Health{Status: modulesdk.HealthDegraded, Reason: m.lastErr.Error()}
	}
	if time.Since(m.lastCollected) > 2*m.interval {
		return modulesdk.Health{
			Status: modulesdk.HealthDegraded,
			Reason: "inventory stale since " + m.lastCollected.Format(time.RFC3339),
		}
	}
	return modulesdk.Health{Status: modulesdk.HealthHealthy}
}

func (m *Module) currentInterval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.interval
}

type policyDoc struct {
	IntervalSeconds int `json:"interval_seconds"`
}

func (m *Module) applyPolicy(doc modulesdk.PolicyDocument) {
	if len(doc.Data) == 0 {
		return
	}
	var p policyDoc
	if err := json.Unmarshal(doc.Data, &p); err != nil {
		m.host.Log().Warn("ignoring malformed policy", "revision", doc.Revision, "err", err)
		return
	}
	if p.IntervalSeconds > 0 {
		m.mu.Lock()
		m.interval = time.Duration(p.IntervalSeconds) * time.Second
		m.mu.Unlock()
		m.host.Log().Info("policy applied", "revision", doc.Revision, "interval", p.IntervalSeconds)
	}
}

// collectAndReport gathers the inventory, stores the snapshot, publishes
// it on the bus, and heartbeats the platform.
func (m *Module) collectAndReport(ctx context.Context) {
	inv, err := Collect()
	m.mu.Lock()
	m.lastCollected = time.Now()
	m.lastErr = err
	m.mu.Unlock()
	if err != nil {
		m.host.Log().Warn("collection failed", "err", err)
		return
	}

	data, err := json.Marshal(inv)
	if err != nil {
		m.host.Log().Error("marshalling inventory", "err", err)
		return
	}

	if err := m.host.Store("").Put(ctx, "last-inventory", data); err != nil {
		m.host.Log().Warn("storing inventory", "err", err)
	}
	if err := m.host.Events().Publish(ctx, "inventory", data); err != nil {
		m.host.Log().Warn("publishing inventory", "err", err)
	}
	delivered, err := m.host.Transport().Send(ctx, modulesdk.Message{
		Peer: modulesdk.PeerGateWeave,
		Kind: "heartbeat",
		Data: data,
	}, true)
	if err != nil {
		m.host.Log().Warn("heartbeat failed", "err", err)
	} else if !delivered {
		m.host.Log().Debug("heartbeat queued: no peer connected")
	}
}
