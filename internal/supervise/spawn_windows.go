package supervise

import (
	"os"
	"os/exec"
	"unsafe"

	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"golang.org/x/sys/windows"
)

// jobHandle wraps a Job Object with kill-on-close: when core dies, the
// handle closes and every module process dies with it. Contain (spec §8).
type jobHandle struct {
	h windows.Handle
}

func baseEnv() []string { return os.Environ() }

// applyPrivilege: restricted-token spawning lands with the verify
// milestone; the Job Object below is the containment piece.
func applyPrivilege(cmd *exec.Cmd, m *manifest.Manifest) error {
	_ = cmd
	_ = m
	return nil
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
