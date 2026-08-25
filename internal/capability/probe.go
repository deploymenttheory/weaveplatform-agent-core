// Package capability performs the one host inventory at startup. Modules
// declare what they require; core probes once and never launches a module
// whose requirements are unmet. No module calls a probe at its own call
// site — that rule turns a panic on an unbound symbol into a startup log
// line.
package capability

import (
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/platform"
)

// Set is the probed capability inventory: name → attributes.
type Set map[string]map[string]string

// Has reports whether every named capability is present.
func (s Set) Has(names ...string) (missing []string) {
	for _, n := range names {
		if _, ok := s[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing
}

// Probe inventories the host. It runs once, at core startup.
func Probe() Set {
	info := platform.Host()
	set := Set{
		// platform.osinfo: the host can describe itself. Always present;
		// its attributes carry what was learned.
		"platform.osinfo": {
			"os":       info.OS,
			"arch":     info.Arch,
			"version":  info.Version,
			"build":    info.Build,
			"hostname": info.Hostname,
		},
	}
	probeOS(set)
	return set
}
