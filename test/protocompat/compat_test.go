// Package protocompat is the permanent spec §11 fixture: the test that
// matters is not that a module runs — it is that core at protocol N
// accepts a module built against N-1 and cleanly refuses one outside the
// window. The v1/ directory is pinned to protocol-1 tags and never
// deleted; every future protocol adds a directory beside it.
package protocompat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/core"
	agentv1 "github.com/deploymenttheory/weaveplatform-agent/sdk/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-agent/sdk/handshake"
	"github.com/deploymenttheory/weaveplatform-agent/sdk/modulesdk/testkit"
)

// TestEveryWindowMemberHasFixture is the invariant that keeps §11 honest as
// the window moves: for every protocol in core's advertised window there
// must be a pinned vN/ fixture directory, so a bump can never quietly drop
// compat coverage for a protocol core still claims to support.
func TestEveryWindowMemberHasFixture(t *testing.T) {
	for p := core.Window.Min; p <= core.Window.Max; p++ {
		dir := fmt.Sprintf("v%d", p)
		if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
			t.Errorf("core advertises protocol %d but %s/main.go is missing: add the pinned fixture",
				p, dir)
		}
	}
}

// buildFixture builds the pinned protocol-1 module OUTSIDE the workspace
// so its go.mod pins, not the checked-out trees, decide what it links.
func buildFixture(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "compat-v1")
	if runtime.GOOS == "windows" {
		// Windows cannot exec a binary without its extension.
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "v1"
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building pinned fixture: %v\n%s", err, out)
	}
	return bin
}

func TestProtocol1AcceptedInWindow(t *testing.T) {
	bin := buildFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Current window {1,1} and the future N-1 shape {1,2}: both must
	// accept a protocol-1 module through Init and Health.
	for _, window := range []handshake.Window{{Min: 1, Max: 1}, {Min: 1, Max: 2}} {
		core := &testkit.StubCore{ModuleID: "compat", Window: window}
		proc, err := core.Launch(ctx, bin)
		if err != nil {
			t.Fatalf("window %+v: launch: %v", window, err)
		}
		if _, err := core.Init(ctx, proc); err != nil {
			proc.Kill()
			t.Fatalf("window %+v: init: %v", window, err)
		}
		h, err := proc.Client.Health(ctx, &agentv1.HealthRequest{})
		if err != nil || h.GetHealth().GetStatus() != agentv1.Health_STATUS_HEALTHY {
			proc.Kill()
			t.Fatalf("window %+v: health: %v %v", window, h, err)
		}
		proc.Kill()
	}
}

func TestProtocol1RefusedOutsideWindow(t *testing.T) {
	bin := buildFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A core whose window has moved past protocol 1 (the N-3 shape) must
	// be refused cleanly: exit 78, no listen, no crash loop.
	core := &testkit.StubCore{ModuleID: "compat", Window: handshake.Window{Min: 2, Max: 4}}
	_, err := core.Launch(ctx, bin)
	if !errors.Is(err, testkit.ErrProtocolRefused) {
		t.Fatalf("window [2,4]: err = %v, want clean protocol refusal", err)
	}
}
