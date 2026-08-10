package supervise

import (
	"time"

	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
)

// State is a supervised module's lifecycle state as reported to operators.
type State string

const (
	// StatePending: registered, not yet launched.
	StatePending State = "pending"
	// StateStarting: spawn through handshake through Start.
	StateStarting State = "starting"
	// StateRunning: started and passing health checks.
	StateRunning State = "running"
	// StateBackoff: crashed; waiting out the restart delay.
	StateBackoff State = "backoff"
	// StateBreaker: crash-looped past the threshold; not restarting.
	// Fail closed: an operator (or a rollback) resets it.
	StateBreaker State = "breaker"
	// StateUnsupportedProtocol: the module refused the advertised window
	// (exit 78). Restarting cannot help; not a crash loop.
	StateUnsupportedProtocol State = "unsupported-protocol"
	// StateRequirementsUnmet: the manifest requires capabilities this
	// host lacks. Never launched.
	StateRequirementsUnmet State = "requirements-unmet"
	// StateStopped: cleanly stopped (core shutdown or operator).
	StateStopped State = "stopped"
)

// Status is one module's supervision snapshot.
type Status struct {
	ID       string
	Version  string
	Protocol uint32
	State    State
	// Detail explains non-running states (missing caps, breaker cause).
	Detail   string
	PID      int
	Restarts uint32
	// Health is the last poll result; zero value when never polled.
	Health   *agentv1.Health
	Since    time.Time
	Surfaces []*agentv1.Surface
}
