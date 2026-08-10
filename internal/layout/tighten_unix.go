//go:build !windows

package layout

import "os"

// tightenDir forces a directory to 0700 even if it pre-existed with looser
// bits. On Windows, ACLs (not mode bits) govern access and Go's chmod is a
// near-no-op, so that variant is a no-op and the installer sets the ACL.
func tightenDir(dir string) error {
	return os.Chmod(dir, 0o700)
}
