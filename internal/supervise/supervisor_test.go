package supervise

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/capability"
	"github.com/deploymenttheory/weaveplatform-agent/internal/eventbus"
	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent/internal/layout"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"github.com/deploymenttheory/weaveplatform-sdk/handshake"
	"github.com/deploymenttheory/weaveplatform-sdk/retry"
)

func buildTestModule(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "testmod"+exeSuffix())
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/testmodule")
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test module: %v\n%s", err, out)
	}
	return bin
}

// exeSuffix: Windows cannot exec a binary without its extension.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func testManifest(caps ...string) *manifest.Manifest {
	return &manifest.Manifest{
		Schema:       1,
		ID:           "testmod",
		Version:      "0.0.1",
		Protocol:     1,
		Zone:         "A",
		Privilege:    manifest.PrivilegeService,
		Session:      manifest.SessionSystem,
		Platforms:    []manifest.Platform{{OS: runtime.GOOS, Arch: runtime.GOARCH}},
		Capabilities: caps,
	}
}

func newTestSupervisor(t *testing.T) *Supervisor {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	// Not t.TempDir(): long test names push unix socket paths past the
	// 104-byte sun_path limit on macOS.
	dir, err := os.MkdirTemp("", "wv-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	lay := layout.Resolve(dir)
	if err := lay.Ensure(); err != nil {
		t.Fatal(err)
	}
	return &Supervisor{
		Log:    log,
		Window: handshake.Window{Min: 1, Max: 1},
		Caps:   capability.Probe(),
		Services: &hostserv.Services{
			Log:       log,
			Bus:       eventbus.New(),
			Store:     hostserv.NewMemStore(),
			Policy:    hostserv.NewMemPolicy(),
			Identity:  hostserv.NewStubIdentity(),
			Transport: &hostserv.LogTransport{Log: log},
		},
		Layout:           lay,
		Verifier:         VerifierFunc(func(string, *manifest.Manifest) error { return nil }),
		Backoff:          retry.Backoff{Initial: 20 * time.Millisecond, Max: 50 * time.Millisecond, Factor: 2},
		BreakerThreshold: 3,
		BreakerWindow:    time.Minute,
		HealthInterval:   100 * time.Millisecond,
		LaunchTimeout:    15 * time.Second,
		StableAfter:      time.Hour, // never reset crash counts inside a test
	}
}

// waitFor polls until pred accepts the module's status or the deadline
// passes.
func waitFor(t *testing.T, sup *Supervisor, desc string, timeout time.Duration, pred func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last Status
	for time.Now().Before(deadline) {
		for _, st := range sup.Statuses() {
			last = st
			if pred(st) {
				return st
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("module never reached %s; last: %+v", desc, last)
	return Status{}
}

// waitState polls until the module reaches want or the deadline passes.
func waitState(t *testing.T, sup *Supervisor, want State, timeout time.Duration) Status {
	t.Helper()
	return waitFor(t, sup, string(want), timeout, func(st Status) bool { return st.State == want })
}

func TestModuleRunsAndRestartsAfterKill(t *testing.T) {
	bin := buildTestModule(t)
	sup := newTestSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.SetBaseContext(ctx)

	if err := sup.Add(Spec{Manifest: testManifest("platform.osinfo"), BinPath: bin}); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, sup, StateRunning, 15*time.Second)
	if st.PID == 0 {
		t.Fatal("running module has no PID")
	}

	// Kill it: the supervisor must notice, back off, and restart.
	proc, err := os.FindProcess(st.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	st2 := waitFor(t, sup, "restarted and running", 15*time.Second, func(s Status) bool {
		return s.State == StateRunning && s.Restarts >= 1 && s.PID != st.PID
	})
	if st2.PID == 0 {
		t.Fatal("restarted module has no PID")
	}

	cancel()
	sup.Wait()
	final := sup.Statuses()[0]
	if final.State != StateStopped {
		t.Errorf("state after shutdown = %s, want stopped", final.State)
	}
}

func TestBreakerTripsOnCrashLoop(t *testing.T) {
	bin := buildTestModule(t)
	sup := newTestSupervisor(t)
	t.Setenv("TESTMOD_EXIT_AFTER_MS", "50")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.SetBaseContext(ctx)

	if err := sup.Add(Spec{Manifest: testManifest("platform.osinfo"), BinPath: bin}); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, sup, StateBreaker, 30*time.Second)
	if st.Restarts < 3 {
		t.Errorf("breaker tripped after %d restarts, want >= 3", st.Restarts)
	}
	cancel()
	sup.Wait()
}

func TestRequirementsUnmetNeverLaunches(t *testing.T) {
	bin := buildTestModule(t)
	sup := newTestSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.SetBaseContext(ctx)

	if err := sup.Add(Spec{Manifest: testManifest("no.such.capability"), BinPath: bin}); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, sup, StateRequirementsUnmet, 5*time.Second)
	if st.PID != 0 {
		t.Errorf("gated module has a PID: it was launched")
	}
	cancel()
	sup.Wait()
}

func TestVerifierRefusalBlocksLaunch(t *testing.T) {
	bin := buildTestModule(t)
	sup := newTestSupervisor(t)
	sup.Verifier = VerifierFunc(func(path string, _ *manifest.Manifest) error {
		return os.ErrPermission
	})
	// A refused verify is a launch failure: backoff, then breaker. It must
	// never run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.SetBaseContext(ctx)

	if err := sup.Add(Spec{Manifest: testManifest("platform.osinfo"), BinPath: bin}); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, sup, StateBreaker, 30*time.Second)
	if st.PID != 0 {
		t.Errorf("refused module has a PID: it was executed")
	}
	cancel()
	sup.Wait()
}

func TestAddRefusesDuplicateID(t *testing.T) {
	bin := buildTestModule(t)
	sup := newTestSupervisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.SetBaseContext(ctx)

	spec := Spec{Manifest: testManifest("platform.osinfo"), BinPath: bin}
	if err := sup.Add(spec); err != nil {
		t.Fatal(err)
	}
	// A second Add for the same id must be refused rather than silently
	// overwriting the runner and orphaning the live process.
	if err := sup.Add(spec); err == nil {
		t.Fatal("duplicate Add accepted; the first runner would be orphaned")
	}
	cancel()
	sup.Wait()
}

func TestAddRequiresBaseContext(t *testing.T) {
	bin := buildTestModule(t)
	sup := newTestSupervisor(t)
	// No SetBaseContext: Add must refuse rather than derive a runner from
	// a nil (or per-call) context.
	if err := sup.Add(Spec{Manifest: testManifest("platform.osinfo"), BinPath: bin}); err == nil {
		t.Fatal("Add accepted without a base context")
	}
}
