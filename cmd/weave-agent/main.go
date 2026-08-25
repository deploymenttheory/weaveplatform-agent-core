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
	"github.com/deploymenttheory/weaveplatform-agent/sdk/wlog"
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
	channelDir := flag.String("channel-dir", os.Getenv("WEAVE_CHANNEL_DIR"),
		"directory holding a signed channel-manifest bundle to verify modules against (offline installs)")
	channelPub := flag.String("channel-pub", os.Getenv("WEAVE_CHANNEL_PUB"),
		"path to the public key a host must prove possession of to drive this guest over the hypervisor channel (default: the platform path)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	log := wlog.Default("weave-agent")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// An offline install — a VM guest, an air-gapped host — has no manifest
	// server to fetch from, so the signed channel manifest ships beside the
	// modules and anchors verification locally. Falling back to the platform
	// verifier when it is absent is safe: those fail closed where there is no
	// signature story (Linux) and check the OS signature where there is.
	verifier := verify.New(log)
	if *channelDir != "" {
		v, err := verify.NewChannelFromDir(log, *rootPub, *channelDir)
		if err != nil {
			log.Error("channel-anchored verification unavailable", "err", err)
			os.Exit(1)
		}
		verifier = v
	}

	err := core.Run(ctx, core.Options{
		StateDir:       *stateDir,
		ModulesDir:     *modulesDir,
		Verifier:       verifier,
		GateWeaveURL:   *gateweaveURL,
		PolicyInterval: *policyInterval,
		ManifestURL:    *manifestURL,
		RootPubPath:    *rootPub,
		ChannelPubPath: *channelPub,
		Log:            log,
	})
	if err != nil {
		log.Error("core failed", "err", err)
		os.Exit(1)
	}
}
