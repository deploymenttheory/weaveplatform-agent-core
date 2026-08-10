package supervise

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
	"github.com/deploymenttheory/weaveplatform-sdk/handshake"
	"github.com/deploymenttheory/weaveplatform-sdk/ipc"
	"google.golang.org/grpc"
)

// errProtocolUnsupported: the module exited 78 — clean refusal, never
// restarted.
var errProtocolUnsupported = errors.New("module refused protocol window")

// proc is one live module process with its harness.
type proc struct {
	cmd      *exec.Cmd
	conn     *grpc.ClientConn
	client   agentv1.ModuleServiceClient
	hostSrv  *grpc.Server
	line     handshake.Line
	initResp *agentv1.InitResponse
	job      jobHandle
	exited   chan error // closed after cmd.Wait returns
}

// launch takes a module from binary to started: verify, host services up,
// spawn, handshake, Init, Start.
func (r *runner) launch(ctx context.Context) (*proc, error) {
	spec := r.spec
	log := r.log

	// 1. Verify before exec. Non-negotiable.
	if err := r.sup.Verifier.Verify(spec.BinPath, spec.Manifest); err != nil {
		return nil, fmt.Errorf("signature verification: %w", err)
	}

	// 2. Per-module socket dir, cleaned of any predecessor's leavings.
	sockDir := r.sup.Layout.ModuleRunDir(spec.Manifest.ID)
	if err := os.RemoveAll(sockDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		return nil, err
	}

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(nonce[:])

	// 3. Host services for this module.
	hostAddr := filepath.Join(sockDir, "host.sock")
	if runtime.GOOS == "windows" {
		hostAddr = `\\.\pipe\weave-host-` + spec.Manifest.ID + "-" + token[:8]
	}
	hostLis, err := ipc.Listen(hostAddr)
	if err != nil {
		return nil, fmt.Errorf("host listener: %w", err)
	}
	hostSrv := r.sup.Services.NewServer(spec.Manifest.ID, token)
	go hostSrv.Serve(hostLis) //nolint:errcheck // ends with Stop

	fail := func(err error) (*proc, error) {
		hostSrv.Stop()
		return nil, err
	}

	// 4. Spawn with the handshake environment and dropped privilege.
	cmd := exec.Command(spec.BinPath)
	cmd.Env = append(baseEnv(),
		fmt.Sprintf("%s=%d", handshake.EnvProtocolMin, r.sup.Window.Min),
		fmt.Sprintf("%s=%d", handshake.EnvProtocolMax, r.sup.Window.Max),
		handshake.EnvToken+"="+token,
		handshake.EnvHostAddr+"="+hostAddr,
		handshake.EnvSocketDir+"="+sockDir,
	)
	if err := applyPrivilege(cmd, spec.Manifest); err != nil {
		return fail(fmt.Errorf("privilege setup: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fail(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fail(err)
	}
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("exec: %w", err))
	}
	job, err := postSpawn(cmd)
	if err != nil {
		cmd.Process.Kill() //nolint:errcheck
		return fail(fmt.Errorf("process containment: %w", err))
	}

	// Module stderr → core's log, attributed.
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			log.Info("module stderr", "line", sc.Text())
		}
	}()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	// 5. The handshake line, bounded.
	lineCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() {
			lineCh <- sc.Text()
			io.Copy(io.Discard, stdout) //nolint:errcheck
		}
	}()

	cleanupProc := func(err error) (*proc, error) {
		killProc(cmd, job)
		hostSrv.Stop()
		return nil, err
	}

	var raw string
	select {
	case raw = <-lineCh:
	case werr := <-exited:
		hostSrv.Stop()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == handshake.ExitProtocolUnsupported {
			return nil, errProtocolUnsupported
		}
		return nil, fmt.Errorf("module exited before handshake: %v", werr)
	case <-time.After(r.sup.launchTimeout()):
		return cleanupProc(errors.New("timeout waiting for handshake line"))
	case <-ctx.Done():
		return cleanupProc(ctx.Err())
	}

	line, err := handshake.Parse(raw)
	if err != nil {
		return cleanupProc(err)
	}
	if !r.sup.Window.Contains(line.Protocol) {
		return cleanupProc(fmt.Errorf("module claimed protocol %d outside window [%d,%d]",
			line.Protocol, r.sup.Window.Min, r.sup.Window.Max))
	}

	// 6. Dial and Init.
	conn, err := ipc.GRPCClient(line.Network, line.Addr)
	if err != nil {
		return cleanupProc(err)
	}
	client := agentv1.NewModuleServiceClient(conn)

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	initResp, err := client.Init(initCtx, &agentv1.InitRequest{
		Protocol:     line.Protocol,
		ModuleId:     spec.Manifest.ID,
		Capabilities: r.sup.capabilityList(),
		Config:       spec.Config,
		Privilege:    privilegeLevel(spec.Manifest),
	})
	if err != nil {
		conn.Close() //nolint:errcheck
		return cleanupProc(fmt.Errorf("init: %w", err))
	}

	// Runtime confirmation of requirements. The manifest gated launch;
	// a module declaring more at runtime than it manifests is refused.
	var missing []string
	for _, c := range initResp.GetRequires() {
		if _, ok := r.sup.Caps[c.GetName()]; !ok {
			missing = append(missing, c.GetName())
		}
	}
	if len(missing) > 0 {
		conn.Close() //nolint:errcheck
		return cleanupProc(fmt.Errorf("runtime requirements unmet: %v", missing))
	}

	startCtx, cancelStart := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStart()
	if _, err := client.Start(startCtx, &agentv1.StartRequest{}); err != nil {
		conn.Close() //nolint:errcheck
		return cleanupProc(fmt.Errorf("start: %w", err))
	}

	return &proc{
		cmd:      cmd,
		conn:     conn,
		client:   client,
		hostSrv:  hostSrv,
		line:     line,
		initResp: initResp,
		job:      job,
		exited:   exited,
	}, nil
}

// stop winds a proc down: Stop with drain deadline, Shutdown, then kill
// after grace.
func (p *proc) stop(log logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := p.client.Stop(ctx, &agentv1.StopRequest{DeadlineSeconds: 10}); err != nil {
		log.Warn("module stop failed", "err", err)
	}
	if _, err := p.client.Shutdown(ctx, &agentv1.ShutdownRequest{}); err != nil {
		log.Warn("module shutdown failed", "err", err)
	}
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
		log.Warn("module did not exit after shutdown; killing")
		killProc(p.cmd, p.job)
		<-p.exited
	}
	p.teardown()
}

// kill hard-stops the process.
func (p *proc) kill() {
	killProc(p.cmd, p.job)
	<-p.exited
	p.teardown()
}

func (p *proc) teardown() {
	p.conn.Close() //nolint:errcheck
	p.hostSrv.Stop()
	closeJob(p.job)
}

func privilegeLevel(m *manifest.Manifest) agentv1.PrivilegeLevel {
	switch m.Privilege {
	case manifest.PrivilegeSystem:
		return agentv1.PrivilegeLevel_PRIVILEGE_LEVEL_SYSTEM
	case manifest.PrivilegeUser:
		return agentv1.PrivilegeLevel_PRIVILEGE_LEVEL_USER
	default:
		return agentv1.PrivilegeLevel_PRIVILEGE_LEVEL_SERVICE
	}
}

// logger is the slog subset launch/stop need; keeps testing simple.
type logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}
