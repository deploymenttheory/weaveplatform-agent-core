//go:build !windows

package supervise

import (
	"os"
	"os/exec"

	"github.com/deploymenttheory/weaveplatform-api/manifest"
)

// jobHandle is Windows-only containment; a no-op here. Orphans on unix die
// with core because modules hold the host socket — a module whose core
// vanishes loses every host service and exits on its own; the supervisor
// also sweeps the module run dirs at startup.
type jobHandle struct{}

func baseEnv() []string { return os.Environ() }

// applyPrivilege drops credentials per the manifest. Actual setuid/setgid
// to a service account lands with the verify milestone; today the manifest
// is honoured for what core *can* enforce (nothing to drop when core runs
// unprivileged in dev).
func applyPrivilege(cmd *exec.Cmd, m *manifest.Manifest) error {
	_ = cmd
	_ = m
	return nil
}

func postSpawn(cmd *exec.Cmd) (jobHandle, error) {
	_ = cmd
	return jobHandle{}, nil
}

func killProc(cmd *exec.Cmd, _ jobHandle) {
	if cmd.Process != nil {
		cmd.Process.Kill() //nolint:errcheck
	}
}

func closeJob(_ jobHandle) {}
