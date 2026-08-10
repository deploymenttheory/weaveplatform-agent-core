//go:build !windows

package weaveboot

import (
	"os"
	"syscall"
)

// terminateSignal asks core to shut down gracefully.
var terminateSignal os.Signal = syscall.SIGTERM
