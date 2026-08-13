// Package core wires the agent together and runs it. Assembly and the run
// loop only — everything with behaviour lives in its own package.
package core

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/capability"
	"github.com/deploymenttheory/weaveplatform-agent/internal/controlsock"
	"github.com/deploymenttheory/weaveplatform-agent/internal/eventbus"
	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent/internal/identity"
	"github.com/deploymenttheory/weaveplatform-agent/internal/layout"
	"github.com/deploymenttheory/weaveplatform-agent/internal/lifecycle"
	"github.com/deploymenttheory/weaveplatform-agent/internal/policy"
	"github.com/deploymenttheory/weaveplatform-agent/internal/store"
	"github.com/deploymenttheory/weaveplatform-agent/internal/store/keyprotect"
	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-agent/internal/transport"
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
	// GateWeaveURL is the platform service's base URL (the stub until
	// GateWeave exists); core derives /v1/policy, /v1/enroll and
	// /v1/messages from it. Empty runs offline: cached policy, ephemeral
	// identity, queued sends.
	GateWeaveURL string
	// PolicyInterval overrides the poll cadence (dev; zero = default).
	PolicyInterval time.Duration
	// ManifestURL is where the signed channel manifest bundle lives.
	ManifestURL string
	// RootPubPath loads the manifest root public key from a file (dev/
	// self-hosted); release packaging embeds it instead.
	RootPubPath string
	// ChannelPubPath is the Ed25519 public key a host must prove possession
	// of before it may drive this guest over the hypervisor channel. Empty
	// takes the platform default (transport.DefaultChannelKeyPath), which is
	// where a guest image provisions it.
	ChannelPubPath string
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

	base := strings.TrimSuffix(opts.GateWeaveURL, "/")
	endpoint := func(p string) string {
		if base == "" {
			return ""
		}
		return base + p
	}

	policyMgr := &policy.Manager{
		Log:      log,
		URL:      endpoint("/v1/policy"),
		Interval: opts.PolicyInterval,
		Cache:    st,
	}
	policyMgr.Load()
	go policyMgr.Run(ctx)

	ident := &identity.Provider{
		Log:       log,
		Store:     st,
		EnrollURL: endpoint("/v1/enroll"),
		// Dev builds allow http + an unpinned enrolment server key; release
		// requires https and the pinned key (S10). ServerPub wiring lands
		// with the enrolment-key provisioning story.
		AllowInsecureEnroll: allowRootPubOverride,
	}
	if err := ident.Init(); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	if err := ident.Enroll(ctx); err != nil {
		// Enrolment failure is not fatal: run ephemeral, retry later.
		log.Warn("enrolment failed; continuing unenrolled", "err", err)
	}

	mux := &transport.Mux{Log: log, Queue: st}
	if base != "" {
		mux.GateWeave = &transport.HTTPPeer{
			URL: endpoint("/v1/messages"),
			Device: func() string {
				id, _, _ := ident.WhoAmI(context.Background())
				return id
			},
		}
	}
	// When running inside a Weave-enabled guest, the hypervisor channel is a
	// second peer (the attested one). Core owns the single connection; guest
	// modules address PEER_HYPERVISOR and never touch the wire. Runs for the
	// core lifetime.
	if attrs, ok := caps["hypervisor.channel"]; ok {
		if err := mux.ConnectHypervisor(ctx, attrs, opts.ChannelPubPath); err != nil {
			log.Warn("hypervisor channel present but could not connect", "err", err)
		} else {
			log.Info("hypervisor channel connected", "device", attrs["device"])
		}
	}

	services := &hostserv.Services{
		Log:       log,
		Bus:       eventbus.New(),
		Store:     st,
		Policy:    policyMgr,
		Identity:  ident,
		Transport: mux,
	}

	sup := &supervise.Supervisor{
		Log:      log,
		Window:   Window,
		Caps:     caps,
		Services: services,
		Layout:   lay,
		Verifier: verifier,
		// Ask modules to ping every 10s; a module silent for 20s (two
		// intervals) is restarted as hung. Catches hangs that Health
		// polling, answered on a separate goroutine, would miss.
		WatchdogInterval: 10 * time.Second,
	}
	// Close the watchdog loop: each module's pings reach the supervisor
	// through this seam (set after sup exists to break the construction
	// cycle between services and sup).
	services.Watchdog = sup.Keepalive
	// Modules live on the core run-lifetime context, never on the context
	// of whatever operation (boot loop, control-socket install) spawned
	// them.
	sup.SetBaseContext(ctx)
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
		if err := sup.Add(spec); err != nil {
			log.Error("registering module failed", "module", spec.Manifest.ID, "err", err)
		}
	}

	lcm := &lifecycle.Manager{
		Log:         log,
		Layout:      lay,
		Verifier:    verifier,
		Supervisor:  sup,
		ManifestURL: opts.ManifestURL,
		SeqStore:    st,
	}
	// The manifest root key is baked into release builds (embeddedRootPub)
	// so the trust anchor can't be swapped by pointing core at a file;
	// only dev builds honour --manifest-root-pub.
	rootPubBytes := embeddedRootPub
	if opts.RootPubPath != "" {
		if !allowRootPubOverride {
			log.Warn("ignoring --manifest-root-pub: release builds use the embedded root key")
		} else {
			data, err := os.ReadFile(opts.RootPubPath)
			if err != nil {
				return fmt.Errorf("reading manifest root key: %w", err)
			}
			rootPubBytes = data
		}
	}
	if len(bytes.TrimSpace(rootPubBytes)) > 0 {
		_, raw, err := manifest.ParsePublicKey(rootPubBytes)
		if err != nil {
			return fmt.Errorf("manifest root key: %w", err)
		}
		lcm.RootPub = raw
	} else {
		log.Warn("no manifest root key configured; channel installs are disabled")
	}

	ctl := &controlsock.Server{
		Log:        log,
		Supervisor: sup,
		Lifecycle:  lcm,
		Window:     Window,
		Identity:   ident,
		StartedAt:  time.Now(),
	}
	ctlErr := make(chan error, 1)
	go func() { ctlErr <- ctl.Serve(ctx, lay.ControlSocket()) }()

	// Readiness signal to weaveboot: once modules are registered and the
	// control socket is being served, this core is up. weaveboot promotes a
	// staged core on this signal (health-gated) rather than on a blind
	// uptime timer — a core that reaches here proves it loads and configures.
	signalReady(log)

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

// envReadyFile is the path weaveboot asks core to write its readiness
// marker to (weaveboot sets it before exec). Empty when core is run
// directly (dev), in which case signalReady is a no-op. Must match
// weaveboot.EnvReadyFile.
const envReadyFile = "WEAVE_READY_FILE"

// signalReady writes core's version to the readiness marker weaveboot
// watches, atomically (temp + rename). weaveboot cleared any prior marker
// before launch, so its presence means this run reached steady state.
// Failure to write is logged, never fatal — a missing marker only costs
// weaveboot its faster promotion path; it falls back to the uptime timer.
func signalReady(log *slog.Logger) {
	path := os.Getenv(envReadyFile)
	if path == "" {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(version.Version+"\n"), 0o600); err != nil {
		log.Warn("writing readiness marker failed", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil { //nolint:gosec // atomic publish of a marker core itself wrote
		log.Warn("publishing readiness marker failed", "err", err)
		os.Remove(tmp) //nolint:errcheck
	}
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
		// Versioned layout (installed by the lifecycle manager): a
		// `current` file names the active version directory.
		if cur, err := os.ReadFile(filepath.Join(mdir, "current")); err == nil {
			mdir = filepath.Join(mdir, "versions", strings.TrimSpace(string(cur)))
		}
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
			return nil, fmt.Errorf(
				"module %s: manifest present but no binary found in %s",
				m.ID,
				mdir,
			)
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
