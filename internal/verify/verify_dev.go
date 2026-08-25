//go:build dev

package verify

import (
	"log/slog"

	"github.com/deploymenttheory/weaveplatform-agent-core/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/manifest"
)

// newVerifier (dev builds only): accept unsigned binaries, loudly. This
// code path does not exist in release builds.
func newVerifier(log *slog.Logger) supervise.Verifier {
	return supervise.VerifierFunc(func(path string, m *manifest.Manifest) error {
		log.Warn("DEV BUILD: signature verification bypassed", "module", m.ID, "path", path)
		return nil
	})
}

// coreVerifier (dev builds only): accept any staged core, loudly.
func coreVerifier(log *slog.Logger) func(string) error {
	return func(path string) error {
		log.Warn("DEV BUILD: core signature verification bypassed", "path", path)
		return nil
	}
}
