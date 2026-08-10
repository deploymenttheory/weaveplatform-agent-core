//go:build !windows

package controlsock

import (
	"fmt"
	"os"

	"github.com/deploymenttheory/weaveplatform-sdk/ipc"
)

// controlAuthorizer permits only root or core's own user to drive the
// control service. A later per-user portal endpoint gets its own,
// group-scoped socket rather than loosening this one.
func controlAuthorizer() ipc.Authorizer {
	self := uint32(os.Getuid())
	return func(p ipc.PeerCred) error {
		if !p.HasUID {
			return nil
		}
		if p.UID == 0 || p.UID == self {
			return nil
		}
		return fmt.Errorf("control socket: peer uid %d not authorized", p.UID)
	}
}
