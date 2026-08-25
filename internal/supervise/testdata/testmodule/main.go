// Command testmodule is the supervisor's integration-test module. It stays
// healthy by default; TESTMOD_EXIT_AFTER_MS makes it crash after start so
// tests can exercise the restart path and the breaker quickly.
package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/modulesdk"
)

type testmod struct {
	crashAfterMS int
}

func (*testmod) ID() string { return "testmod" }

func (*testmod) Requires() []modulesdk.Capability {
	return []modulesdk.Capability{"platform.osinfo"}
}

// SetConfig accepts {"crash_after_ms": N} so lifecycle tests can install
// a "broken" version declaratively.
func (m *testmod) SetConfig(doc []byte) error {
	var cfg struct {
		CrashAfterMS int `json:"crash_after_ms"`
	}
	if len(doc) > 0 {
		if err := json.Unmarshal(doc, &cfg); err != nil {
			return err
		}
	}
	m.crashAfterMS = cfg.CrashAfterMS
	return nil
}

func (*testmod) Init(ctx context.Context, host modulesdk.Host) error { return nil }

func (m *testmod) Start(ctx context.Context) error {
	crash := m.crashAfterMS
	if ms, err := strconv.Atoi(os.Getenv("TESTMOD_EXIT_AFTER_MS")); err == nil && ms > 0 {
		crash = ms
	}
	if crash > 0 {
		go func() {
			time.Sleep(time.Duration(crash) * time.Millisecond)
			os.Exit(1)
		}()
	}
	return nil
}

func (*testmod) Stop(ctx context.Context) error { return nil }

func (*testmod) Health() modulesdk.Health {
	return modulesdk.Health{Status: modulesdk.HealthHealthy}
}

func main() { modulesdk.Serve(&testmod{}) }
