package ipc

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
)

const network = NetworkPipe

func listen(addr string) (net.Listener, error) {
	// Default security descriptor: the creating user. Tightened SDDLs for
	// privilege-dropped modules land with the supervisor's privilege work.
	return winio.ListenPipe(addr, nil)
}

func dial(ctx context.Context, netw, addr string) (net.Conn, error) {
	if netw != NetworkPipe {
		return nil, fmt.Errorf("ipc: network %q not supported on this platform", netw)
	}
	return winio.DialPipeContext(ctx, addr)
}
