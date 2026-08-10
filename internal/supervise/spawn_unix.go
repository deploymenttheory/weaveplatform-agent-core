//go:build !windows

package supervise

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"syscall"

	"github.com/deploymenttheory/weaveplatform-api/manifest"
)

// jobHandle is Windows-only containment; a no-op here. Orphans on unix die
// with core because modules hold the host socket — a module whose core
// vanishes loses every host service and exits on its own; the supervisor
// also sweeps the module run dirs at startup.
type jobHandle struct{}

func baseEnv() []string { return os.Environ() }

// serviceAccount is the unprivileged account modules with privilege
// "service" run as when core itself is root. The installer creates it.
func serviceAccount() string {
	if runtime.GOOS == "darwin" {
		return "_weaveagent"
	}
	return "weave-agent"
}

// applyPrivilege drops credentials per the manifest. Fail closed: a
// manifest asking for a drop core cannot perform refuses the launch.
func applyPrivilege(cmd *exec.Cmd, m *manifest.Manifest) error {
	if m.Session != manifest.SessionSystem {
		return fmt.Errorf("session placement %q not yet supported by the supervisor", m.Session)
	}
	if os.Geteuid() != 0 {
		// Nothing to drop and nothing to drop to: dev runs, where core
		// itself is unprivileged. The manifest's declaration is already
		// satisfied or exceeded.
		return nil
	}
	switch m.Privilege {
	case manifest.PrivilegeSystem:
		return nil
	case manifest.PrivilegeService:
		u, err := user.Lookup(serviceAccount())
		if err != nil {
			return fmt.Errorf("service account %q missing (installer creates it): %w", serviceAccount(), err)
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return err
		}
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return err
		}
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
		return nil
	default:
		return fmt.Errorf("privilege %q needs per-user session support, not yet implemented", m.Privilege)
	}
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
