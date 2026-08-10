// Package manifest carries the Go types for the module manifest
// (schema/module-manifest.schema.json): the document every module authors,
// embeds, and publishes, and that core gates launch decisions on.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Privilege levels a manifest may declare. Not every module is root.
const (
	PrivilegeSystem  = "system"
	PrivilegeService = "service"
	PrivilegeUser    = "user"
)

// Session placements a manifest may declare.
const (
	SessionSystem         = "system"
	SessionPerUserConsole = "per-user-console"
	SessionPerUserAll     = "per-user-all"
)

// Platform is one os/arch pair a module ships for.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// Signing pins the identity core verifies before exec.
type Signing struct {
	AppleTeamID         string `json:"apple_team_id,omitempty"`
	AuthenticodeSubject string `json:"authenticode_subject,omitempty"`
}

// Artifact is one published binary, stamped by CI.
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// Manifest is a module's declaration. See the JSON schema for field
// semantics.
type Manifest struct {
	Schema       int        `json:"schema"`
	ID           string     `json:"id"`
	Version      string     `json:"version"`
	Protocol     uint32     `json:"protocol"`
	Zone         string     `json:"zone"`
	Privilege    string     `json:"privilege"`
	Session      string     `json:"session"`
	Platforms    []Platform `json:"platforms"`
	Capabilities []string   `json:"capabilities,omitempty"`
	Signing      *Signing   `json:"signing,omitempty"`
	Artifacts    []Artifact `json:"artifacts,omitempty"`
}

var (
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	digestRe  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// Parse unmarshals and validates a manifest document.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Load reads and parses a manifest file.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Validate checks structural invariants the schema encodes.
func (m *Manifest) Validate() error {
	switch {
	case m.Schema != 1:
		return fmt.Errorf("manifest: unsupported schema %d", m.Schema)
	case !idRe.MatchString(m.ID):
		return fmt.Errorf("manifest: invalid id %q", m.ID)
	case !versionRe.MatchString(m.Version):
		return fmt.Errorf("manifest: invalid version %q", m.Version)
	case m.Protocol < 1:
		return fmt.Errorf("manifest: protocol must be >= 1")
	case m.Zone != "A" && m.Zone != "B" && m.Zone != "C":
		return fmt.Errorf("manifest: invalid zone %q", m.Zone)
	case m.Privilege != PrivilegeSystem && m.Privilege != PrivilegeService && m.Privilege != PrivilegeUser:
		return fmt.Errorf("manifest: invalid privilege %q", m.Privilege)
	case m.Session != SessionSystem && m.Session != SessionPerUserConsole && m.Session != SessionPerUserAll:
		return fmt.Errorf("manifest: invalid session %q", m.Session)
	case len(m.Platforms) == 0:
		return fmt.Errorf("manifest: at least one platform required")
	}
	for _, p := range m.Platforms {
		if err := osArch(p.OS, p.Arch); err != nil {
			return err
		}
	}
	for _, a := range m.Artifacts {
		if err := osArch(a.OS, a.Arch); err != nil {
			return err
		}
		if !digestRe.MatchString(a.Digest) {
			return fmt.Errorf("manifest: invalid digest %q", a.Digest)
		}
	}
	return nil
}

// SupportsHost reports whether the manifest lists the given os/arch.
func (m *Manifest) SupportsHost(goos, goarch string) bool {
	for _, p := range m.Platforms {
		if p.OS == goos && p.Arch == goarch {
			return true
		}
	}
	return false
}

func osArch(o, a string) error {
	switch o {
	case "darwin", "windows", "linux":
	default:
		return fmt.Errorf("manifest: invalid os %q", o)
	}
	switch a {
	case "arm64", "amd64":
	default:
		return fmt.Errorf("manifest: invalid arch %q", a)
	}
	return nil
}
