package sysinfo

import (
	"time"

	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/platform"
)

// Inventory is one collected snapshot of the host.
type Inventory struct {
	CollectedAt time.Time `json:"collected_at"`
	Hostname    string    `json:"hostname"`
	OS          string    `json:"os"`
	Arch        string    `json:"arch"`
	OSVersion   string    `json:"os_version"`
	OSBuild     string    `json:"os_build,omitempty"`
	// Filled per-OS where readable without privilege.
	HardwareModel string `json:"hardware_model,omitempty"`
	SerialNumber  string `json:"serial_number,omitempty"`
	MemoryBytes   uint64 `json:"memory_bytes,omitempty"`
	UptimeSeconds uint64 `json:"uptime_seconds,omitempty"`
	CPUModel      string `json:"cpu_model,omitempty"`
	CPUCores      int    `json:"cpu_cores,omitempty"`
}

// Collect gathers the inventory. Fields that cannot be read stay empty —
// partial inventory is data, not an error; only a total failure errors.
func Collect() (*Inventory, error) {
	info := platform.Host()
	inv := &Inventory{
		CollectedAt: time.Now().UTC(),
		Hostname:    info.Hostname,
		OS:          info.OS,
		Arch:        info.Arch,
		OSVersion:   info.Version,
		OSBuild:     info.Build,
	}
	collectOS(inv)
	return inv, nil
}
