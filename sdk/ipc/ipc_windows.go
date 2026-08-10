package ipc

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

const network = NetworkPipe

// pipeSDDL restricts the pipe to SYSTEM and the Administrators group:
//
//	D:  discretionary ACL
//	(A;;GA;;;SY)  allow generic-all to SYSTEM
//	(A;;GA;;;BA)  allow generic-all to Builtin Administrators
//
// This replaces go-winio's default (which grants broad access), closing
// the "any local user can open the control/host pipe" hole. Modules that
// run at lower privilege need a wider SDDL; that is set per-listener via
// ListenPipeSDDL when per-module Windows privilege lands.
const pipeSDDL = "D:(A;;GA;;;SY)(A;;GA;;;BA)"

func listen(addr string) (net.Listener, error) {
	return winio.ListenPipe(addr, &winio.PipeConfig{SecurityDescriptor: pipeSDDL})
}

// ListenPipeSDDL creates a named-pipe listener with an explicit SDDL, for
// callers (privilege-dropped modules) that need a wider descriptor than
// the SYSTEM+Administrators default.
func ListenPipeSDDL(addr, sddl string) (net.Listener, error) {
	return winio.ListenPipe(addr, &winio.PipeConfig{SecurityDescriptor: sddl})
}

func dial(ctx context.Context, netw, addr string) (net.Conn, error) {
	if netw != NetworkPipe {
		return nil, fmt.Errorf("ipc: network %q not supported on this platform", netw)
	}
	return winio.DialPipeContext(ctx, addr)
}
