package weaveboot

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildFake builds a fake core: "good" sleeps until signalled, "bad"
// exits 1 immediately.
func buildFake(t *testing.T, dir, kind string) string {
	t.Helper()
	src := filepath.Join(dir, kind+".go")
	var code string
	if kind == "good" {
		code = `package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, os.Interrupt)
	<-ch
}
`
	} else {
		code = "package main\n\nfunc main() { panic(\"broken core\") }\n"
	}
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, kind)
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", kind, err, out)
	}
	return bin
}

func install(t *testing.T, coreDir, version, bin string) {
	t.Helper()
	dst := filepath.Join(coreDir, "versions", version)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, binaryName()), data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStagedPromoteAndCrashLoopRevert(t *testing.T) {
	work := t.TempDir()
	good := buildFake(t, work, "good")
	bad := buildFake(t, work, "bad")

	coreDir := filepath.Join(work, "core")
	// v1 (good) is installed and current.
	install(t, coreDir, "1.0.0", good)
	writeStr(filepath.Join(coreDir, "current"), "1.0.0")
	// v2 (broken) is staged, as an updater would leave it.
	stage := filepath.Join(coreDir, "staging", "2.0.0")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(bad) //nolint:errcheck
	if err := os.WriteFile(filepath.Join(stage, binaryName()), data, 0o755); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Log:             log,
			CoreDir:         coreDir,
			StableAfter:     5 * time.Second,
			RevertThreshold: 3,
			Backoff:         50 * time.Millisecond,
			VerifyCore:      func(string) error { return nil }, // test verifier
		})
	}()

	// The staged broken 2.0.0 is promoted, crash-loops, and weaveboot
	// reverts to 1.0.0, which stays up.
	deadline := time.Now().Add(45 * time.Second)
	reverted := false
	for time.Now().Before(deadline) {
		cur, err := os.ReadFile(filepath.Join(coreDir, "current"))
		if err == nil && string(cur) == "1.0.0\n" {
			// Reverted. Give the good core a moment to be running.
			if prev, err := os.ReadFile(filepath.Join(coreDir, "previous")); err == nil && len(prev) > 0 {
				reverted = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !reverted {
		t.Fatal("weaveboot never reverted to 1.0.0")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("weaveboot returned error: %v", err)
	}
}
