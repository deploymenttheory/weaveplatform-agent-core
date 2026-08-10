// Command testmodule is the supervisor's integration-test module. It stays
// healthy by default; TESTMOD_EXIT_AFTER_MS makes it crash after start so
// tests can exercise the restart path and the breaker quickly.
package main

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/deploymenttheory/weaveplatform-sdk/modulesdk"
)

type testmod struct{}

func (testmod) ID() string { return "testmod" }

func (testmod) Requires() []modulesdk.Capability {
	return []modulesdk.Capability{"platform.osinfo"}
}

func (testmod) Init(ctx context.Context, host modulesdk.Host) error { return nil }

func (testmod) Start(ctx context.Context) error {
	if ms, err := strconv.Atoi(os.Getenv("TESTMOD_EXIT_AFTER_MS")); err == nil && ms > 0 {
		go func() {
			time.Sleep(time.Duration(ms) * time.Millisecond)
			os.Exit(1)
		}()
	}
	return nil
}

func (testmod) Stop(ctx context.Context) error { return nil }

func (testmod) Health() modulesdk.Health {
	return modulesdk.Health{Status: modulesdk.HealthHealthy}
}

func main() { modulesdk.Serve(testmod{}) }
