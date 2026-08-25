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

	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/ipc"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/manifest"
)

// jobHandle is Windows-only containment; a no-op here. Unix containment is
// Pdeathsig (Linux) plus the SDK's host-connection watchdog (all unix), so
// a module dies when core dies rather than orphaning.
type jobHandle struct{}

func baseEnv() []string { return os.Environ() }

// serviceAccount is the unprivileged account "service"-privilege modules
// run as when core is root. Phase 2 makes this per-module; today it is one
// shared account created by the installer.
func serviceAccount() string {
	if runtime.GOOS == "darwin" {
		return "_weaveagent"
	}
	return "weave-agent"
}

// dropCreds resolves the uid/gid a module should drop to. ok is false when
// core is not root (dev runs) or the module is system-privilege — in both
// cases there is nothing to drop. It fails closed on a drop it cannot
// perform.
func dropCreds(m *manifest.Manifest) (uid, gid uint32, ok bool, err error) {
	if m.Session != manifest.SessionSystem {
		return 0, 0, false, fmt.Errorf(
			"session placement %q not yet supported by the supervisor",
			m.Session,
		)
	}
	if os.Geteuid() != 0 {
		return 0, 0, false, nil
	}
	switch m.Privilege {
	case manifest.PrivilegeSystem:
		return 0, 0, false, nil
	case manifest.PrivilegeService:
		u, lerr := user.Lookup(serviceAccount())
		if lerr != nil {
			return 0, 0, false, fmt.Errorf(
				"service account %q missing (installer creates it): %w",
				serviceAccount(),
				lerr,
			)
		}
		uid64, perr := strconv.ParseUint(u.Uid, 10, 32)
		if perr != nil {
			return 0, 0, false, perr
		}
		gid64, perr := strconv.ParseUint(u.Gid, 10, 32)
		if perr != nil {
			return 0, 0, false, perr
		}
		return uint32(uid64), uint32(gid64), true, nil
	default:
		return 0, 0, false, fmt.Errorf(
			"privilege %q needs per-user session support, not yet implemented",
			m.Privilege,
		)
	}
}

// applyPrivilege sets the child's credentials (when dropping) and, on
// Linux, Pdeathsig so an orphaned module is killed when core dies. The
// returned cleanup exists to match the Windows signature (which must close
// a token handle after Start); on unix it is a no-op.
func applyPrivilege(cmd *exec.Cmd, m *manifest.Manifest) (cleanup func(), err error) {
	cleanup = func() {}
	uid, gid, ok, err := dropCreds(m)
	if err != nil {
		return cleanup, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	setPdeathsig(cmd.SysProcAttr)
	if ok {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: gid}
	}
	return cleanup, nil
}

// prepareSocketDir makes a module's dropped uid able to create its own
// listener in, and dial host.sock inside, the per-module socket dir. Core
// owns the dir 0700; without this chown a dropped module EACCESes and the
// whole privilege-drop path is dead (it was). No-op when not dropping.
func prepareSocketDir(dir, hostAddr string, m *manifest.Manifest) error {
	uid, gid, ok, err := dropCreds(m)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := os.Chown(dir, int(uid), int(gid)); err != nil {
		return fmt.Errorf("chown module socket dir: %w", err)
	}
	if err := os.Chown(hostAddr, int(uid), int(gid)); err != nil {
		return fmt.Errorf("chown host socket: %w", err)
	}
	return nil
}

// authorizeModulePeer authorizes connections to a module's host socket by
// peer uid: root, core's own uid, or the module's dropped uid. This is the
// real defence the env token only imitated — a same-uid neighbour can read
// the token from /proc but cannot present a different uid to the kernel.
func authorizeModulePeer(m *manifest.Manifest) ipc.Authorizer {
	allowed := map[uint32]bool{0: true, uint32(os.Getuid()): true}
	if uid, _, ok, err := dropCreds(m); err == nil && ok {
		allowed[uid] = true
	}
	return func(p ipc.PeerCred) error {
		if !p.HasUID {
			return nil // can't determine on this platform; dir perms gate
		}
		if allowed[p.UID] {
			return nil
		}
		return fmt.Errorf("peer uid %d not authorized for module %s", p.UID, m.ID)
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
