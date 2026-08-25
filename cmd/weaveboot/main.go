// Command weaveboot supervises core so core can be replaced in place:
// staged, health-gated, with rollback. launchd/SCM own weaveboot only;
// weaveboot owns core; core owns modules.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/deploymenttheory/weaveplatform-agent-core/internal/layout"
	"github.com/deploymenttheory/weaveplatform-agent-core/internal/verify"
	"github.com/deploymenttheory/weaveplatform-agent-core/internal/weaveboot"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/wlog"
)

func main() {
	stateDir := flag.String("state-dir", "", "override the state directory (also WEAVE_STATE_DIR)")
	flag.Parse()

	log := wlog.Default("weaveboot")
	lay := layout.Resolve(*stateDir)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// Everything after -- goes to weave-agent; the state dir always does.
	agentArgs := append([]string{"--state-dir", lay.StateDir}, flag.Args()...)
	err := weaveboot.Run(ctx, weaveboot.Options{
		Log:        log,
		CoreDir:    filepath.Join(lay.StateDir, "core"),
		AgentArgs:  agentArgs,
		VerifyCore: verify.Core(log),
	})
	if err != nil {
		log.Error("weaveboot failed", "err", err)
		os.Exit(1)
	}
}
