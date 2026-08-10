//go:build unix

package transport

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"golang.org/x/term"
)

// openDevice opens the probed hypervisor device node. On macOS the channel is
// a calling-unit tty (/dev/cu.*) whose line discipline would corrupt the frame
// protocol, so it is put into raw mode; a Linux virtio-ports node is a plain
// character device and is left as-is.
func openDevice(attrs map[string]string) (io.ReadWriteCloser, error) {
	dev := attrs["device"]
	if dev == "" {
		return nil, fmt.Errorf("no hypervisor device in probe attributes")
	}
	f, err := os.OpenFile(dev, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	if term.IsTerminal(int(f.Fd())) {
		if _, err := term.MakeRaw(int(f.Fd())); err != nil {
			f.Close() //nolint:errcheck
			return nil, fmt.Errorf("raw mode on %s: %w", dev, err)
		}
	}
	return f, nil
}
