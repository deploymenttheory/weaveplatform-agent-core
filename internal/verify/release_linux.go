//go:build !dev

package verify

import (
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
)

// newVerifier (release, Linux): no signature story exists yet for Linux
// module binaries, so release builds fail closed until one does
// (detached-signature verification against the channel manifest key).
func newVerifier(_ *slog.Logger) supervise.Verifier {
	return supervise.VerifierFunc(func(path string, _ *manifest.Manifest) error {
		return fmt.Errorf("verify: no Linux signature verification yet; refusing %s (dev builds: -tags dev)", path)
	})
}
