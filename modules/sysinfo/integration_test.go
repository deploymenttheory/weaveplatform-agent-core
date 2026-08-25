package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	agentv1 "github.com/deploymenttheory/weaveplatform-agent-core/sdk/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/modulesdk/testkit"
)

// TestSysinfoUnderStubCore drives the built module binary through the real
// handshake and asserts it exercises every host surface.
func TestSysinfoUnderStubCore(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "sysinfo")
	if runtime.GOOS == "windows" {
		// Windows cannot exec a binary without its extension.
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(build.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	core := &testkit.StubCore{
		ModuleID:     "sysinfo",
		Capabilities: []string{"platform.osinfo"},
		Config:       []byte(`{"interval_seconds": 1}`),
	}
	proc, err := core.Launch(ctx, bin)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer proc.Kill()

	initResp, err := core.Init(ctx, proc)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(initResp.GetRequires()) != 1 || initResp.GetRequires()[0].GetName() != "platform.osinfo" {
		t.Errorf("requires = %v", initResp.GetRequires())
	}
	if len(initResp.GetSurfaces()) != 1 || initResp.GetSurfaces()[0].GetId() != "inventory" {
		t.Errorf("surfaces = %v", initResp.GetSurfaces())
	}

	if _, err := proc.Client.Start(ctx, &agentv1.StartRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}

	// The collect job runs immediately at Start: within a couple of
	// seconds the stub must have seen the store write, the bus publish
	// and the heartbeat.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := proc.Data.StoreGet("last-inventory"); ok &&
			len(proc.Data.Events()) > 0 && len(proc.Data.Sends()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, ok := proc.Data.StoreGet("last-inventory"); !ok {
		t.Error("no inventory stored")
	}
	evs := proc.Data.Events()
	if len(evs) == 0 || evs[0].Topic != "sysinfo.inventory" {
		t.Errorf("events = %v, want sysinfo.inventory", evs)
	}
	sends := proc.Data.Sends()
	if len(sends) == 0 || sends[0].Kind != "heartbeat" {
		t.Errorf("sends = %v, want heartbeat", sends)
	}

	h, err := proc.Client.Health(ctx, &agentv1.HealthRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if h.GetHealth().GetStatus() != agentv1.Health_STATUS_HEALTHY {
		t.Errorf("health = %v (%s)", h.GetHealth().GetStatus(), h.GetHealth().GetReason())
	}

	if err := proc.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
