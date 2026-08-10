//go:build !dev

package verify

import (
	"bytes"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
)

// newVerifier (release, macOS).
func newVerifier(_ *slog.Logger) supervise.Verifier {
	return supervise.VerifierFunc(codesignVerify)
}

// codesignVerify authenticates a module binary on macOS. Two checks, both
// against the pinned absolute /usr/bin/codesign (SecStaticCode would need
// cgo; codesign uses the same trust store and is boring):
//
//  1. `codesign --verify --strict=all` — the signature is valid and the
//     binary unmodified.
//  2. The TeamIdentifier from `codesign -dvv` equals the manifest's
//     pinned apple_team_id. Ad-hoc signatures have no team and are
//     refused: fetched modules must carry a real signing identity.
func codesignVerify(path string, m *manifest.Manifest) error {
	if m.Signing == nil || m.Signing.AppleTeamID == "" {
		return fmt.Errorf("verify: manifest for %s pins no apple_team_id; refusing", m.ID)
	}

	verify := exec.Command("/usr/bin/codesign", "--verify", "--strict=all", path)
	var verr bytes.Buffer
	verify.Stderr = &verr
	if err := verify.Run(); err != nil {
		return fmt.Errorf("verify: codesign rejected %s: %s", path, strings.TrimSpace(verr.String()))
	}

	display := exec.Command("/usr/bin/codesign", "-d", "-vv", path)
	var out bytes.Buffer
	// codesign -d writes its details to stderr.
	display.Stderr = &out
	if err := display.Run(); err != nil {
		return fmt.Errorf("verify: codesign -d failed for %s: %w", path, err)
	}
	team, err := parseTeamIdentifier(out.String())
	if err != nil {
		return fmt.Errorf("verify: %s: %w", path, err)
	}
	if team != m.Signing.AppleTeamID {
		return fmt.Errorf("verify: %s signed by team %q, manifest pins %q", path, team, m.Signing.AppleTeamID)
	}
	return nil
}

// parseTeamIdentifier extracts TeamIdentifier=XXXX from codesign -dvv
// output. "not set" (platform and ad-hoc binaries) is an error: there is
// no identity to pin.
func parseTeamIdentifier(out string) (string, error) {
	for line := range strings.Lines(out) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok {
			v = strings.TrimSpace(v)
			if v == "" || v == "not set" {
				return "", fmt.Errorf("signature carries no team identifier")
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("no TeamIdentifier in codesign output")
}
