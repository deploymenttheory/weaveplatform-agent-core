package supervise

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/foundation"
	winsec "github.com/deploymenttheory/go-bindings-win32/bindings/win32/security"
	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/system/threading"
	"github.com/deploymenttheory/weaveplatform-agent/sdk/ipc"
	"github.com/deploymenttheory/weaveplatform-agent/sdk/manifest"
	"golang.org/x/sys/windows"
)

// jobHandle wraps a Job Object with kill-on-close: when core dies, the
// handle closes and every module process dies with it. Contain (spec §8).
type jobHandle struct {
	h windows.Handle
}

func baseEnv() []string { return os.Environ() }

// prepareSocketDir is a no-op on Windows (named pipes carry an SDDL, not
// filesystem ownership; the restrictive SDDL is Phase 2 work).
func prepareSocketDir(_, _ string, _ *manifest.Manifest) error { return nil }

// applyPrivilege drops credentials per the manifest. On Windows the
// "service" drop is a restricted primary token — same user, maximum
// privileges removed (DISABLE_MAX_PRIVILEGE) — assigned to the child via
// SysProcAttr.Token. It returns a cleanup that MUST be called after
// cmd.Start: os/exec does not take ownership of a caller-supplied token
// handle, so without this it leaks one handle per launch — and launches
// are a hot loop in a privileged service.
func applyPrivilege(cmd *exec.Cmd, m *manifest.Manifest) (cleanup func(), err error) {
	cleanup = func() {}
	if m.Session != manifest.SessionSystem {
		return cleanup, fmt.Errorf("session placement %q not yet supported by the supervisor", m.Session)
	}
	switch m.Privilege {
	case manifest.PrivilegeSystem:
		return cleanup, nil
	case manifest.PrivilegeService:
		var procToken foundation.HANDLE
		if err := threading.OpenProcessToken(threading.GetCurrentProcess(),
			winsec.TOKEN_ACCESS_MASK(windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY),
			&procToken); err != nil {
			return cleanup, fmt.Errorf("opening process token: %w", err)
		}
		defer windows.CloseHandle(windows.Handle(procToken)) //nolint:errcheck
		var restricted foundation.HANDLE
		if err := winsec.CreateRestrictedToken(procToken, winsec.DISABLE_MAX_PRIVILEGE,
			nil, nil, nil, &restricted); err != nil {
			return cleanup, fmt.Errorf("creating restricted token: %w", err)
		}
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.Token = syscall.Token(restricted)
		return func() { windows.CloseHandle(windows.Handle(restricted)) }, nil //nolint:errcheck
	default:
		return cleanup, fmt.Errorf("privilege %q needs per-user session support, not yet implemented", m.Privilege)
	}
}

// authorizeModulePeer on Windows is allow-all: named-pipe access is gated
// by the pipe SDDL (S11), and peer uid is not available via the net.Conn
// here. See WINDOWS_HANDOFF.md for the GetNamedPipeClientProcessId path.
func authorizeModulePeer(_ *manifest.Manifest) ipc.Authorizer {
	return func(ipc.PeerCred) error { return nil }
}

func postSpawn(cmd *exec.Cmd) (jobHandle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return jobHandle{}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job) //nolint:errcheck
		return jobHandle{}, err
	}
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job) //nolint:errcheck
		return jobHandle{}, err
	}
	defer windows.CloseHandle(proc) //nolint:errcheck
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		windows.CloseHandle(job) //nolint:errcheck
		return jobHandle{}, err
	}
	return jobHandle{h: job}, nil
}

func killProc(cmd *exec.Cmd, job jobHandle) {
	if job.h != 0 {
		// Terminating the job kills the whole tree.
		windows.TerminateJobObject(job.h, 1) //nolint:errcheck
		return
	}
	if cmd.Process != nil {
		cmd.Process.Kill() //nolint:errcheck
	}
}

func closeJob(job jobHandle) {
	if job.h != 0 {
		windows.CloseHandle(job.h) //nolint:errcheck
	}
}
