// Package supervise owns module processes: spawn, verify-before-exec,
// handshake, health, crash-loop damping, and the circuit breaker. It fails
// closed: a module that cannot run safely does not run.
package supervise

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/capability"
	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent/internal/layout"
	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"github.com/deploymenttheory/weaveplatform-sdk/handshake"
	"github.com/deploymenttheory/weaveplatform-sdk/retry"
)

// Spec is one module the supervisor should run.
type Spec struct {
	Manifest *manifest.Manifest
	BinPath  string
	// Config is delivered opaquely in InitRequest.
	Config []byte
}

// Supervisor runs a set of modules. Configure the exported fields before
// Run; they are not mutated afterwards.
type Supervisor struct {
	Log      *slog.Logger
	Window   handshake.Window
	Caps     capability.Set
	Services *hostserv.Services
	Layout   layout.Layout
	Verifier Verifier

	// Backoff dampens restarts. Zero value gets retry.New().
	Backoff retry.Backoff
	// BreakerThreshold restarts within BreakerWindow trip the breaker.
	// Zero gets 5 in 10 minutes.
	BreakerThreshold int
	BreakerWindow    time.Duration
	// HealthInterval is the default poll cadence when a module doesn't
	// request one. Zero gets 30s.
	HealthInterval time.Duration
	// HealthStrikes consecutive failed polls restart the module. Zero
	// gets 3.
	HealthStrikes int
	// LaunchTimeout bounds spawn-to-handshake. Zero gets 10s.
	LaunchTimeout time.Duration
	// StableAfter is how long a module must stay up before its crash
	// count resets. Zero gets 60s.
	StableAfter time.Duration

	mu      sync.Mutex
	runners map[string]*runner
}

func (s *Supervisor) launchTimeout() time.Duration {
	if s.LaunchTimeout > 0 {
		return s.LaunchTimeout
	}
	return 10 * time.Second
}

func (s *Supervisor) breakerThreshold() int {
	if s.BreakerThreshold > 0 {
		return s.BreakerThreshold
	}
	return 5
}

func (s *Supervisor) breakerWindow() time.Duration {
	if s.BreakerWindow > 0 {
		return s.BreakerWindow
	}
	return 10 * time.Minute
}

func (s *Supervisor) healthInterval() time.Duration {
	if s.HealthInterval > 0 {
		return s.HealthInterval
	}
	return 30 * time.Second
}

func (s *Supervisor) healthStrikes() int {
	if s.HealthStrikes > 0 {
		return s.HealthStrikes
	}
	return 3
}

func (s *Supervisor) stableAfter() time.Duration {
	if s.StableAfter > 0 {
		return s.StableAfter
	}
	return 60 * time.Second
}

func (s *Supervisor) backoff() retry.Backoff {
	if s.Backoff != (retry.Backoff{}) {
		return s.Backoff
	}
	return retry.New()
}

func (s *Supervisor) capabilityList() []*agentv1.Capability {
	out := make([]*agentv1.Capability, 0, len(s.Caps))
	for name, attrs := range s.Caps {
		out = append(out, &agentv1.Capability{Name: name, Attributes: attrs})
	}
	return out
}

// Add registers a module. Pre-launch gates run here: platform support and
// the capability probe. Gated modules are registered (visible to
// operators) but never launched.
func (s *Supervisor) Add(ctx context.Context, spec Spec) {
	runCtx, cancel := context.WithCancel(ctx)
	r := &runner{
		sup:    s,
		spec:   spec,
		log:    s.Log.With("module", spec.Manifest.ID),
		state:  StatePending,
		since:  time.Now(),
		cancel: cancel,
	}
	s.mu.Lock()
	if s.runners == nil {
		s.runners = make(map[string]*runner)
	}
	s.runners[spec.Manifest.ID] = r
	s.mu.Unlock()

	if missing := s.Caps.Has(spec.Manifest.Capabilities...); len(missing) > 0 {
		r.setState(StateRequirementsUnmet, "missing capabilities: "+joinStrings(missing))
		r.log.Warn("module not launched: requirements unmet", "missing", missing)
		return
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.run(runCtx)
	}()
}

// StopModule winds one module down (drain, shutdown, kill after grace)
// and unregisters it. No-op for unknown ids.
func (s *Supervisor) StopModule(id string) {
	s.mu.Lock()
	r := s.runners[id]
	delete(s.runners, id)
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
}

// Replace hot-swaps a module: the old process drains and stops, then the
// new spec starts. The caller health-gates the result and rolls back on
// failure.
func (s *Supervisor) Replace(ctx context.Context, spec Spec) {
	s.StopModule(spec.Manifest.ID)
	s.Add(ctx, spec)
}

// SweepOrphans clears leftover per-module socket dirs from a previous core
// instance. Call once, before any Add.
func (s *Supervisor) SweepOrphans() {
	dir := filepath.Join(s.Layout.RunDir, "modules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		os.RemoveAll(filepath.Join(dir, e.Name())) //nolint:errcheck
	}
}

// Statuses snapshots every registered module.
func (s *Supervisor) Statuses() []Status {
	s.mu.Lock()
	runners := make([]*runner, 0, len(s.runners))
	for _, r := range s.runners {
		runners = append(runners, r)
	}
	s.mu.Unlock()
	out := make([]Status, 0, len(runners))
	for _, r := range runners {
		out = append(out, r.status())
	}
	return out
}

// Wait blocks until every runner has wound down (after ctx cancellation).
func (s *Supervisor) Wait() {
	s.mu.Lock()
	runners := make([]*runner, 0, len(s.runners))
	for _, r := range s.runners {
		runners = append(runners, r)
	}
	s.mu.Unlock()
	for _, r := range runners {
		r.wg.Wait()
	}
}

// runner is one module's supervision loop.
type runner struct {
	sup    *Supervisor
	spec   Spec
	log    *slog.Logger
	wg     sync.WaitGroup
	cancel context.CancelFunc

	mu       sync.Mutex
	state    State
	detail   string
	since    time.Time
	pid      int
	restarts uint32
	health   *agentv1.Health
	protocol uint32
	surfaces []*agentv1.Surface
}

func (r *runner) setState(st State, detail string) {
	r.mu.Lock()
	r.state = st
	r.detail = detail
	r.since = time.Now()
	r.mu.Unlock()
}

func (r *runner) status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{
		ID:       r.spec.Manifest.ID,
		Version:  r.spec.Manifest.Version,
		Protocol: r.protocol,
		State:    r.state,
		Detail:   r.detail,
		PID:      r.pid,
		Restarts: r.restarts,
		Health:   r.health,
		Since:    r.since,
		Surfaces: r.surfaces,
	}
}

// run is the per-module state machine: launch → monitor → (backoff →
// relaunch)* until stopped, broken, or refused.
func (r *runner) run(ctx context.Context) {
	var crashes []time.Time
	attempt := 0
	backoff := r.sup.backoff()

	for {
		if ctx.Err() != nil {
			r.setState(StateStopped, "core shutting down")
			return
		}
		r.setState(StateStarting, "")
		p, err := r.launch(ctx)
		if err != nil {
			switch {
			case err == errProtocolUnsupported:
				r.setState(StateUnsupportedProtocol, "")
				r.log.Error("module refused protocol window; not restarting")
				return
			case ctx.Err() != nil:
				r.setState(StateStopped, "core shutting down")
				return
			}
			r.log.Error("launch failed", "err", err)
		} else {
			r.mu.Lock()
			r.pid = p.cmd.Process.Pid
			r.protocol = p.line.Protocol
			r.surfaces = p.initResp.GetSurfaces()
			r.mu.Unlock()
			r.setState(StateRunning, "")
			r.log.Info("module running", "pid", p.cmd.Process.Pid, "protocol", p.line.Protocol)

			startedAt := time.Now()
			clean := r.monitor(ctx, p)
			if clean {
				r.setState(StateStopped, "")
				return
			}
			if time.Since(startedAt) >= r.sup.stableAfter() {
				attempt = 0
				crashes = nil
			}
		}

		// Crash accounting and the breaker.
		now := time.Now()
		crashes = append(crashes, now)
		pruned := crashes[:0]
		for _, t := range crashes {
			if now.Sub(t) <= r.sup.breakerWindow() {
				pruned = append(pruned, t)
			}
		}
		crashes = pruned
		r.mu.Lock()
		r.restarts++
		r.mu.Unlock()
		if len(crashes) >= r.sup.breakerThreshold() {
			r.setState(StateBreaker, "crash loop: restarts exhausted")
			r.log.Error("circuit breaker tripped; not restarting",
				"crashes", len(crashes), "window", r.sup.breakerWindow())
			return
		}

		delay := backoff.Delay(attempt)
		attempt++
		r.setState(StateBackoff, "restarting after "+delay.Round(time.Millisecond).String())
		r.log.Warn("module down; backing off", "delay", delay, "attempt", attempt)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			r.setState(StateStopped, "core shutting down")
			return
		}
	}
}

// monitor waits on the process, polling health. Returns true when the exit
// was a clean, requested stop (ctx cancelled), false for crashes.
func (r *runner) monitor(ctx context.Context, p *proc) bool {
	interval := r.sup.healthInterval()
	if s := p.initResp.GetHealthIntervalSeconds(); s > 0 {
		interval = time.Duration(s) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	strikes := 0
	for {
		select {
		case <-ctx.Done():
			r.log.Info("stopping module")
			p.stop(r.log)
			return true
		case err := <-p.exited:
			p.teardown()
			r.log.Warn("module exited", "err", err)
			return false
		case <-ticker.C:
			hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			resp, err := p.client.Health(hctx, &agentv1.HealthRequest{})
			cancel()
			if err != nil {
				strikes++
				r.log.Warn("health poll failed", "err", err, "strikes", strikes)
			} else {
				h := resp.GetHealth()
				r.mu.Lock()
				r.health = h
				r.mu.Unlock()
				switch h.GetStatus() {
				case agentv1.Health_STATUS_UNHEALTHY:
					strikes++
					r.log.Warn("module unhealthy", "reason", h.GetReason(), "strikes", strikes)
				case agentv1.Health_STATUS_DEGRADED:
					strikes = 0
					r.log.Info("module degraded", "reason", h.GetReason())
				default:
					strikes = 0
				}
			}
			if strikes >= r.sup.healthStrikes() {
				r.log.Error("health strikes exhausted; restarting module")
				p.kill()
				return false
			}
		}
	}
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
