// Package core wires the agent together and runs it. Assembly and the run
// loop only — everything with behaviour lives in its own package.
package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/capability"
	"github.com/deploymenttheory/weaveplatform-agent/internal/controlsock"
	"github.com/deploymenttheory/weaveplatform-agent/internal/eventbus"
	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent/internal/layout"
	"github.com/deploymenttheory/weaveplatform-agent/internal/policy"
	"github.com/deploymenttheory/weaveplatform-agent/internal/store"
	"github.com/deploymenttheory/weaveplatform-agent/internal/store/keyprotect"
	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-agent/internal/version"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"github.com/deploymenttheory/weaveplatform-sdk/handshake"
)

// Window is the protocol range this core accepts. Protocol 1 is current;
// the window widens when protocol 2 ships.
var Window = handshake.Window{Min: 1, Max: 1}

// Options configure a core run.
type Options struct {
	// StateDir overrides the platform layout (dev, tests).
	StateDir string
	// ModulesDir is scanned for installed modules: one subdirectory per
	// module containing module.manifest.json and the module binary.
	// Empty means the layout's modules dir.
	ModulesDir string
	// Verifier authenticates module binaries. Nil refuses everything —
	// core fails closed until the verify milestone wires the real one.
	Verifier supervise.Verifier
	// GateWeaveURL is the policy endpoint (e.g. the stub's /v1/policy).
	// Empty runs offline: cached policy only.
	GateWeaveURL string
	// PolicyInterval overrides the poll cadence (dev; zero = default).
	PolicyInterval time.Duration
	Log            *slog.Logger
}

// Run starts core and blocks until ctx ends.
func Run(ctx context.Context, opts Options) error {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	lay := layout.Resolve(opts.StateDir)
	if err := lay.Ensure(); err != nil {
		return fmt.Errorf("preparing state directories: %w", err)
	}
	modulesDir := opts.ModulesDir
	if modulesDir == "" {
		modulesDir = lay.ModulesDir
	}

	caps := capability.Probe()
	log.Info("core starting",
		"version", version.Version,
		"protocol_min", Window.Min, "protocol_max", Window.Max,
		"state_dir", lay.StateDir, "modules_dir", modulesDir,
		"capabilities", len(caps))

	verifier := opts.Verifier
	if verifier == nil {
		verifier = supervise.VerifierFunc(func(path string, _ *manifest.Manifest) error {
			return fmt.Errorf("no verifier configured; refusing to exec %s", path)
		})
	}

	st, err := store.Open(lay.StateDir, keyprotect.New())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	policyMgr := &policy.Manager{
		Log:      log,
		URL:      opts.GateWeaveURL,
		Interval: opts.PolicyInterval,
		Cache:    st,
	}
	policyMgr.Load()
	go policyMgr.Run(ctx)

	identity := hostserv.NewStubIdentity()
	services := &hostserv.Services{
		Log:       log,
		Bus:       eventbus.New(),
		Store:     st,
		Policy:    policyMgr,
		Identity:  identity,
		Transport: &hostserv.LogTransport{Log: log},
	}

	sup := &supervise.Supervisor{
		Log:      log,
		Window:   Window,
		Caps:     caps,
		Services: services,
		Layout:   lay,
		Verifier: verifier,
	}
	sup.SweepOrphans()

	specs, err := discoverModules(modulesDir)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		log.Warn("no modules found", "dir", modulesDir)
	}
	for _, spec := range specs {
		if !spec.Manifest.SupportsHost(runtime.GOOS, runtime.GOARCH) {
			log.Warn("module does not support this host; skipping",
				"module", spec.Manifest.ID, "os", runtime.GOOS, "arch", runtime.GOARCH)
			continue
		}
		sup.Add(ctx, spec)
	}

	ctl := &controlsock.Server{
		Log:        log,
		Supervisor: sup,
		Window:     Window,
		DeviceID:   identity.DeviceID,
		StartedAt:  time.Now(),
	}
	ctlErr := make(chan error, 1)
	go func() { ctlErr <- ctl.Serve(ctx, lay.ControlSocket()) }()

	select {
	case <-ctx.Done():
	case err := <-ctlErr:
		if err != nil {
			return fmt.Errorf("control socket: %w", err)
		}
	}
	log.Info("core stopping")
	sup.Wait()
	return nil
}

// discoverModules scans dir for <id>/module.manifest.json + binary. The
// binary is named after the module id (<id>.exe on Windows), or "module"
// as a fallback.
func discoverModules(dir string) ([]supervise.Spec, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var specs []supervise.Spec
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mdir := filepath.Join(dir, e.Name())
		mpath := filepath.Join(mdir, "module.manifest.json")
		m, err := manifest.Load(mpath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("module %s: %w", e.Name(), err)
		}
		bin := ""
		for _, cand := range binaryCandidates(m.ID) {
			p := filepath.Join(mdir, cand)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				bin = p
				break
			}
		}
		if bin == "" {
			return nil, fmt.Errorf("module %s: manifest present but no binary found in %s", m.ID, mdir)
		}
		var config []byte
		if b, err := os.ReadFile(filepath.Join(mdir, "config.json")); err == nil {
			config = b
		}
		specs = append(specs, supervise.Spec{Manifest: m, BinPath: bin, Config: config})
	}
	return specs, nil
}

func binaryCandidates(id string) []string {
	if runtime.GOOS == "windows" {
		return []string{id + ".exe", "module.exe"}
	}
	return []string{id, "module"}
}
