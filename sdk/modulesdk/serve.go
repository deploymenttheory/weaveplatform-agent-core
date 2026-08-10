package modulesdk

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-sdk/handshake"
	"github.com/deploymenttheory/weaveplatform-sdk/ipc"
	"github.com/deploymenttheory/weaveplatform-sdk/wlog"
	"google.golang.org/grpc"
)

// Serve runs m as a Weave platform module: it reads the handshake
// environment, negotiates the protocol, listens on the module socket,
// answers with the one-line handshake, and serves core's lifecycle RPCs
// until Shutdown or SIGTERM.
//
// A module's main is one line:
//
//	func main() { modulesdk.Serve(sysinfo.New()) }
//
// Serve does not return. A protocol outside core's advertised window exits
// with code 78 (EX_CONFIG) before listening; other failures exit 1.
func Serve(m Module) {
	log := wlog.Default(m.ID())
	if err := serve(m, log); err != nil {
		log.Error("module runtime failed", "err", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func serve(m Module, log *slog.Logger) error {
	// 1. Protocol negotiation: refuse cleanly before touching anything.
	window, err := handshake.ParseWindow(
		os.Getenv(handshake.EnvProtocolMin),
		os.Getenv(handshake.EnvProtocolMax),
	)
	if err != nil {
		return fmt.Errorf("reading protocol window: %w", err)
	}
	if !window.Contains(Protocol) {
		fmt.Fprintf(os.Stderr, "module %s speaks protocol %d, outside advertised window [%d,%d]\n",
			m.ID(), Protocol, window.Min, window.Max)
		os.Exit(handshake.ExitProtocolUnsupported)
	}

	token := os.Getenv(handshake.EnvToken)
	hostAddr := os.Getenv(handshake.EnvHostAddr)
	if token == "" || hostAddr == "" {
		return fmt.Errorf("handshake environment incomplete: %s/%s unset — modules are launched by core, not by hand",
			handshake.EnvToken, handshake.EnvHostAddr)
	}

	// 2. Listen on the module socket.
	addr, err := moduleAddr(m.ID())
	if err != nil {
		return err
	}
	lis, err := ipc.Listen(addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	defer lis.Close()

	// 3. Wire the lifecycle server. The host connection is deferred to
	// Init: core's host listener is guaranteed up by then.
	srv := &moduleServer{
		m:        m,
		shutdown: make(chan struct{}, 1),
		hostLost: make(chan struct{}, 1),
		connectHost: func() (*hostClient, error) {
			return dialHost(ipc.Network(), hostAddr, token, log)
		},
	}
	grpcServer := grpc.NewServer()
	agentv1.RegisterModuleServiceServer(grpcServer, srv)

	errCh := make(chan error, 1)
	go func() { errCh <- grpcServer.Serve(lis) }()

	// 4. Answer the handshake. Exactly one line, then never print to
	// stdout again.
	fmt.Println(handshake.Line{Protocol: Protocol, Network: ipc.Network(), Addr: addr}.Format())

	// 5. Run until Shutdown, a lost core connection, SIGTERM, or server
	// failure.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, os.Interrupt)
	select {
	case <-srv.shutdown:
		log.Info("shutdown requested by core")
	case <-srv.hostLost:
		log.Warn("core connection lost; exiting to avoid orphaning")
	case s := <-sig:
		log.Info("signal received", "signal", s.String())
	case err := <-errCh:
		return fmt.Errorf("grpc server: %w", err)
	}
	grpcServer.GracefulStop()
	if host := srv.getHost(); host != nil {
		host.stopJobs()
		host.Close()
	}
	return nil
}

// moduleAddr picks the module's listen address: a socket in core's
// directory on unix, a uniquely-suffixed pipe name on Windows.
func moduleAddr(id string) (string, error) {
	if runtime.GOOS == "windows" {
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		return `\\.\pipe\weave-` + id + "-" + hex.EncodeToString(nonce[:]), nil
	}
	dir := os.Getenv(handshake.EnvSocketDir)
	if dir == "" {
		return "", fmt.Errorf("%s unset — modules are launched by core, not by hand", handshake.EnvSocketDir)
	}
	return filepath.Join(dir, id+".sock"), nil
}
