package manifest

import (
	"encoding/json"
	"fmt"
	"runtime"
)

// ChannelManifest is the signed document mapping a channel to a known-good
// set of core and module versions and the protocol they assume
// (schema/channel-manifest.schema.json). SaaS follows a rolling manifest;
// self-hosted pins one. Same mechanism, different policy.
type ChannelManifest struct {
	Schema      int               `json:"schema"`
	Channel     string            `json:"channel"`
	GeneratedAt string            `json:"generated_at"`
	Protocol    ProtocolWindow    `json:"protocol"`
	Bindings    map[string]string `json:"bindings,omitempty"`
	Core        ChannelCore       `json:"core"`
	Modules     []ChannelModule   `json:"modules"`
}

// ProtocolWindow is the accepted protocol range the channel assumes.
type ProtocolWindow struct {
	Min uint32 `json:"min"`
	Max uint32 `json:"max"`
}

// ChannelCore describes the core release the channel serves.
type ChannelCore struct {
	Version   string            `json:"version"`
	Artifacts []ChannelArtifact `json:"artifacts"`
}

// ChannelModule is the distribution subset of a module's manifest: enough
// signed data for core to gate fetch and exec decisions before it
// possesses the binary.
type ChannelModule struct {
	ID           string            `json:"id"`
	Version      string            `json:"version"`
	Protocol     uint32            `json:"protocol"`
	Privilege    string            `json:"privilege"`
	Session      string            `json:"session"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Signing      *Signing          `json:"signing,omitempty"`
	Artifacts    []ChannelArtifact `json:"artifacts"`
}

// ChannelArtifact is one downloadable binary.
type ChannelArtifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// ParseChannel unmarshals and validates a channel manifest.
func ParseChannel(data []byte) (*ChannelManifest, error) {
	var c ChannelManifest
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("channel manifest: %w", err)
	}
	if c.Schema != 1 {
		return nil, fmt.Errorf("channel manifest: unsupported schema %d", c.Schema)
	}
	if c.Channel == "" || c.Protocol.Min == 0 || c.Protocol.Max < c.Protocol.Min {
		return nil, fmt.Errorf("channel manifest: missing channel or invalid protocol window")
	}
	return &c, nil
}

// Module returns the named module's entry.
func (c *ChannelManifest) Module(id string) (*ChannelModule, bool) {
	for i := range c.Modules {
		if c.Modules[i].ID == id {
			return &c.Modules[i], true
		}
	}
	return nil, false
}

// ArtifactForHost picks the artifact matching this process's OS/arch.
func ArtifactForHost(arts []ChannelArtifact) (*ChannelArtifact, bool) {
	for i := range arts {
		if arts[i].OS == runtime.GOOS && arts[i].Arch == runtime.GOARCH {
			return &arts[i], true
		}
	}
	return nil, false
}

// ModuleManifest converts the distribution subset back into the manifest
// core's supervisor consumes.
func (cm *ChannelModule) ModuleManifest(platforms []Platform) *Manifest {
	return &Manifest{
		Schema:       1,
		ID:           cm.ID,
		Version:      cm.Version,
		Protocol:     cm.Protocol,
		Zone:         "A",
		Privilege:    cm.Privilege,
		Session:      cm.Session,
		Platforms:    platforms,
		Capabilities: cm.Capabilities,
		Signing:      cm.Signing,
	}
}
