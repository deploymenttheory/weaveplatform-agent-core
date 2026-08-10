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
	gateweaveURL := flag.String("gateweave-url", os.Getenv("WEAVE_GATEWEAVE_URL"),
		"GateWeave policy endpoint; empty runs offline on cached policy")
	policyInterval := flag.Duration("policy-interval", 0, "policy poll interval override (dev)")
	manifestURL := flag.String("manifest-url", os.Getenv("WEAVE_MANIFEST_URL"),
		"base URL of the signed channel manifest bundle")
	rootPub := flag.String("manifest-root-pub", os.Getenv("WEAVE_MANIFEST_ROOT_PUB"),
		"path to the manifest root public key (dev/self-hosted)")
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
		StateDir:       *stateDir,
		ModulesDir:     *modulesDir,
		Verifier:       verify.New(log),
		GateWeaveURL:   *gateweaveURL,
		PolicyInterval: *policyInterval,
		ManifestURL:    *manifestURL,
		RootPubPath:    *rootPub,
		Log:            log,
	})
	if err != nil {
		log.Error("core failed", "err", err)
		os.Exit(1)
	}
}
