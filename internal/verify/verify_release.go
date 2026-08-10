//go:build !dev

package verify

import (
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
)

// newVerifier (release): fail closed until codesign/WinVerifyTrust land.
func newVerifier(_ *slog.Logger) supervise.Verifier {
	return supervise.VerifierFunc(func(path string, _ *manifest.Manifest) error {
		return fmt.Errorf("signature verification not yet implemented in this build; refusing %s (dev builds: -tags dev)", path)
	})
}
