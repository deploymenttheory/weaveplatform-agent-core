//go:build dev

package verify

import (
	"log/slog"

	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
)

// newVerifier (dev builds only): accept unsigned binaries, loudly. This
// code path does not exist in release builds.
func newVerifier(log *slog.Logger) supervise.Verifier {
	return supervise.VerifierFunc(func(path string, m *manifest.Manifest) error {
		log.Warn("DEV BUILD: signature verification bypassed", "module", m.ID, "path", path)
		return nil
	})
}
