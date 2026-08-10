// Command weaveboot supervises core so core can be replaced in place:
// staged, health-gated, with rollback. launchd/SCM own weaveboot only;
// weaveboot owns core; core owns modules.
//
// Implemented in milestone M8. Until then it execs core directly so the
// installed process tree has the final shape from day one.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		fatal("locating self: %v", err)
	}
	agent := filepath.Join(filepath.Dir(self), agentBinaryName())
	if _, err := os.Stat(agent); err != nil {
		fatal("core binary not found beside weaveboot: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	cmd := exec.CommandContext(ctx, agent, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("core exited: %v", err)
	}
}

func agentBinaryName() string {
	if isWindows {
		return "weave-agent.exe"
	}
	return "weave-agent"
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "weaveboot: "+format+"\n", args...)
	os.Exit(1)
}
