// Command weave-agent is core: the one authenticated, policied, supervised
// presence on a machine. Everything product-shaped lives in modules.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/deploymenttheory/weaveplatform-agent/internal/core"
	"github.com/deploymenttheory/weaveplatform-agent/internal/verify"
	"github.com/deploymenttheory/weaveplatform-agent/internal/version"
	"github.com/deploymenttheory/weaveplatform-sdk/wlog"
)

func main() {
	stateDir := flag.String("state-dir", "", "override the state directory (dev/tests; also WEAVE_STATE_DIR)")
	modulesDir := flag.String("modules-dir", "", "override the installed-modules directory")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	log := wlog.Default("weave-agent")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	err := core.Run(ctx, core.Options{
		StateDir:   *stateDir,
		ModulesDir: *modulesDir,
		Verifier:   verify.New(log),
		Log:        log,
	})
	if err != nil {
		log.Error("core failed", "err", err)
		os.Exit(1)
	}
}
