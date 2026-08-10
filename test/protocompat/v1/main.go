// Command weave-protocompat-v1 is the frozen protocol-1 module: built
// from pinned SDK tags, run against every future core in CI to prove the
// spec §11 property — core at protocol N accepts an N-1 module and
// cleanly refuses one outside the window.
package main

import (
	"context"

	"github.com/deploymenttheory/weaveplatform-sdk/modulesdk"
)

type compat struct{}

func (compat) ID() string                                 { return "compat" }
func (compat) Requires() []modulesdk.Capability           { return nil }
func (compat) Init(context.Context, modulesdk.Host) error { return nil }
func (compat) Start(context.Context) error                { return nil }
func (compat) Stop(context.Context) error                 { return nil }
func (compat) Health() modulesdk.Health {
	return modulesdk.Health{Status: modulesdk.HealthHealthy}
}

func main() { modulesdk.Serve(compat{}) }
