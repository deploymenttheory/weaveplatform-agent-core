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
	"strconv"
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
	// VerifyCore authenticates a staged core binary before it is promoted
	// and executed as the (privileged) root of the process tree. It is the
	// same fail-closed discipline modules get. Nil refuses all staged core
	// promotions in release builds; dev builds may pass a permissive one.
	VerifyCore func(binPath string) error
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
			if reverted, prev := revert(o, version); reverted {
				o.Log.Error("core crash-looping; reverted", "from", version, "to", prev)
				crashes = 0
				continue
			}
			o.Log.Error(
				"core crash-looping and cannot revert; continuing to retry",
				"version",
				version,
			)
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
	// On ctx cancel (service stop), send the graceful terminate signal so
	// core runs its shutdown path (drain modules, close the store) instead
	// of the default SIGKILL. WaitDelay then bounds how long we wait before
	// the harder kill. terminateSignal is SIGTERM on unix, Kill on Windows
	// (which has no graceful process signal).
	cmd.Cancel = func() error { return cmd.Process.Signal(terminateSignal) }
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

// promoteStaged promotes exactly one staged version — the highest semver
// with a binary — so lexical ReadDir order can't pick "10.0.0" over
// "9.0.0" or clobber the current/previous chain across multiple staged
// dirs. The old current becomes previous. (S3 adds signature verification
// of the staged core before promotion.)
func promoteStaged(o Options) {
	stagingDir := filepath.Join(o.CoreDir, "staging")
	entries, err := os.ReadDir(stagingDir)
	if err != nil || len(entries) == 0 {
		return
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ver := e.Name()
		if _, err := os.Stat(filepath.Join(stagingDir, ver, binaryName())); err != nil {
			o.Log.Warn("staged core lacks binary; ignoring", "version", ver)
			continue
		}
		if best == "" || higherSemver(ver, best) {
			best = ver
		}
	}
	if best == "" {
		return
	}

	// Verify the staged core before it becomes the privileged root of the
	// process tree — the one component that verifies everything else must
	// not itself be promoted unverified.
	stagedBin := filepath.Join(stagingDir, best, binaryName())
	if o.VerifyCore == nil {
		o.Log.Error("refusing to promote staged core: no verifier configured", "version", best)
		return
	}
	if err := o.VerifyCore(stagedBin); err != nil {
		o.Log.Error("staged core failed verification; not promoting", "version", best, "err", err)
		os.RemoveAll(filepath.Join(stagingDir, best)) //nolint:errcheck
		return
	}

	src := filepath.Join(stagingDir, best)
	dst := filepath.Join(o.CoreDir, "versions", best)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		o.Log.Warn("promote: mkdir versions failed", "err", err)
		return
	}
	os.RemoveAll(dst) //nolint:errcheck
	if err := os.Rename(src, dst); err != nil {
		o.Log.Warn("promoting staged core failed", "version", best, "err", err)
		return
	}
	old := readMarker(o.CoreDir, "current")
	if err := writeStr(filepath.Join(o.CoreDir, "current"), best); err != nil {
		o.Log.Error("promote: writing current failed", "err", err)
		return
	}
	if old != "" && old != best {
		if err := writeStr(filepath.Join(o.CoreDir, "previous"), old); err != nil {
			o.Log.Warn("promote: writing previous failed", "err", err)
		}
	}
	// Clear any other staged versions so a stale lower one can't be
	// promoted on a later boot.
	os.RemoveAll(stagingDir) //nolint:errcheck
	o.Log.Info("staged core promoted", "version", best, "previous", old)
}

// revert flips current back to previous and records the bad version as the
// new previous (so an operator can flip forward). Reverting to the same
// version is terminal — it means previous is also bad — and returns false.
func revert(o Options, badVersion string) (bool, string) {
	v := readMarker(o.CoreDir, "previous")
	if v == "" || v == badVersion {
		return false, ""
	}
	if _, err := os.Stat(filepath.Join(o.CoreDir, "versions", v, binaryName())); err != nil {
		return false, ""
	}
	if err := writeStr(filepath.Join(o.CoreDir, "current"), v); err != nil {
		o.Log.Error("revert: writing current failed", "err", err)
		return false, ""
	}
	// The bad version becomes previous so flip-forward is possible.
	if err := writeStr(filepath.Join(o.CoreDir, "previous"), badVersion); err != nil {
		o.Log.Warn("revert: writing previous failed", "err", err)
	}
	return true, v
}

func readMarker(coreDir, name string) string {
	b, err := os.ReadFile(filepath.Join(coreDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// higherSemver reports whether a > b for dotted numeric versions, falling
// back to string comparison for non-numeric components.
func higherSemver(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ai, aerr := strconv.Atoi(as[i])
		bi, berr := strconv.Atoi(bs[i])
		if aerr == nil && berr == nil {
			if ai != bi {
				return ai > bi
			}
			continue
		}
		if as[i] != bs[i] {
			return as[i] > bs[i]
		}
	}
	return len(as) > len(bs)
}

// writeStr writes a marker atomically (temp + rename): a crash mid-write
// must not truncate current/previous, which would strand the boot loop.
func writeStr(path, s string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(s + "\n"); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	return os.Rename(tmpName, path) //nolint:gosec // tmpName from os.CreateTemp; path is core-internal
}
