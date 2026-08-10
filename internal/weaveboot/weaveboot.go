// Package weaveboot supervises core so core can be replaced in place.
// launchd/SCM own weaveboot only; weaveboot owns core. The installed
// footprint never changes — the binaries behind it do, staged and
// reversible:
//
//	<stateDir>/core/versions/<ver>/weave-agent(.exe)
//	<stateDir>/core/current            version string
//	<stateDir>/core/previous           version string
//	<stateDir>/core/staging/<ver>/     picked up at loop start, promoted
//
// A promoted core that crash-loops before proving stable is reverted to
// the previous version automatically.
package weaveboot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Options configure the boot supervisor.
type Options struct {
	Log *slog.Logger
	// CoreDir is <stateDir>/core.
	CoreDir string
	// AgentArgs are passed through to weave-agent.
	AgentArgs []string
	// StableAfter is how long a core must run to count as good; zero
	// gets 60s.
	StableAfter time.Duration
	// RevertThreshold crashes within a stable window trigger revert to
	// previous; zero gets 3.
	RevertThreshold int
	// Backoff between restarts; zero gets 2s.
	Backoff time.Duration
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "weave-agent.exe"
	}
	return "weave-agent"
}

// Run supervises core until ctx ends.
func Run(ctx context.Context, o Options) error {
	stable := o.StableAfter
	if stable == 0 {
		stable = 60 * time.Second
	}
	threshold := o.RevertThreshold
	if threshold == 0 {
		threshold = 3
	}
	backoff := o.Backoff
	if backoff == 0 {
		backoff = 2 * time.Second
	}

	crashes := 0
	for ctx.Err() == nil {
		promoteStaged(o)

		version, bin, err := currentBinary(o.CoreDir)
		if err != nil {
			return fmt.Errorf("weaveboot: %w", err)
		}
		o.Log.Info("starting core", "version", version, "bin", bin)
		started := time.Now()
		err = runOnce(ctx, bin, o.AgentArgs)
		if ctx.Err() != nil {
			return nil
		}
		o.Log.Warn("core exited", "version", version, "after", time.Since(started), "err", err)

		if time.Since(started) >= stable {
			crashes = 0
			continue
		}
		crashes++
		if crashes >= threshold {
			if reverted, prev := revert(o); reverted {
				o.Log.Error("core crash-looping; reverted", "from", version, "to", prev)
				crashes = 0
				continue
			}
			o.Log.Error("core crash-looping and no previous version; continuing to retry")
			crashes = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
	}
	return nil
}

func runOnce(ctx context.Context, bin string, args []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = 15 * time.Second
	return cmd.Run()
}

// currentBinary resolves the active core binary. Falls back to a binary
// beside weaveboot when no versioned layout exists (dev installs).
func currentBinary(coreDir string) (string, string, error) {
	if cur, err := os.ReadFile(filepath.Join(coreDir, "current")); err == nil {
		v := strings.TrimSpace(string(cur))
		bin := filepath.Join(coreDir, "versions", v, binaryName())
		if _, err := os.Stat(bin); err == nil {
			return v, bin, nil
		}
		return "", "", fmt.Errorf("current version %s has no binary", v)
	}
	self, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	bin := filepath.Join(filepath.Dir(self), binaryName())
	if _, err := os.Stat(bin); err != nil {
		return "", "", fmt.Errorf("no versioned core and no %s beside weaveboot", binaryName())
	}
	return "unversioned", bin, nil
}

// promoteStaged installs anything in staging/: the newest staged version
// becomes current, the old current becomes previous. Verification of the
// staged binary mirrors module verify and rides the same packaging
// signing identity (wired with release packaging).
func promoteStaged(o Options) {
	stagingDir := filepath.Join(o.CoreDir, "staging")
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) == 0 {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ver := e.Name()
		src := filepath.Join(stagingDir, ver)
		if _, err := os.Stat(filepath.Join(src, binaryName())); err != nil {
			o.Log.Warn("staged core lacks binary; ignoring", "version", ver)
			continue
		}
		dst := filepath.Join(o.CoreDir, "versions", ver)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			continue
		}
		os.RemoveAll(dst) //nolint:errcheck
		if err := os.Rename(src, dst); err != nil {
			o.Log.Warn("promoting staged core failed", "version", ver, "err", err)
			continue
		}
		old := ""
		if b, err := os.ReadFile(filepath.Join(o.CoreDir, "current")); err == nil {
			old = strings.TrimSpace(string(b))
		}
		writeStr(filepath.Join(o.CoreDir, "current"), ver)
		if old != "" && old != ver {
			writeStr(filepath.Join(o.CoreDir, "previous"), old)
		}
		o.Log.Info("staged core promoted", "version", ver, "previous", old)
	}
}

// revert flips current back to previous. Returns the version reverted to.
func revert(o Options) (bool, string) {
	prev, err := os.ReadFile(filepath.Join(o.CoreDir, "previous"))
	if err != nil {
		return false, ""
	}
	v := strings.TrimSpace(string(prev))
	if _, err := os.Stat(filepath.Join(o.CoreDir, "versions", v, binaryName())); err != nil {
		return false, ""
	}
	writeStr(filepath.Join(o.CoreDir, "current"), v)
	return true, v
}

func writeStr(path, s string) {
	os.MkdirAll(filepath.Dir(path), 0o700)    //nolint:errcheck
	os.WriteFile(path, []byte(s+"\n"), 0o600) //nolint:errcheck
}
