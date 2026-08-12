//go:build !dev && linux

package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On Linux, dpkg establishes provenance at install; core's exec-time job is to
// confirm nothing has become writable by a non-root party since. These tests
// run unprivileged, so the files are owned by the test user rather than root —
// which is itself one of the conditions that must be refused.

func TestLinuxVerifierRefusesNonRootOwnedBinary(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs an unprivileged run to produce a non-root-owned file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "guestweave")
	if err := os.WriteFile(path, []byte("module"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := newVerifier(nil).Verify(path, nil)
	if err == nil {
		t.Fatal("a module owned by a non-root user was accepted")
	}
	if !strings.Contains(err.Error(), "not root") {
		t.Fatalf("the refusal does not name the reason: %v", err)
	}
}

// A file that is root-owned and tight can still be swapped by anyone who can
// write the directory it sits in, so the directory is checked too. This is the
// case that a file-only check would miss.
func TestLinuxVerifierChecksTheContainingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "guestweave")
	if err := os.WriteFile(path, []byte("module"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := newVerifier(nil).Verify(path, nil); err == nil {
		t.Fatal("a module in a world-writable directory was accepted")
	}
}

func TestLinuxVerifierRefusesWorldWritableBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guestweave")
	if err := os.WriteFile(path, []byte("module"), 0o777); err != nil {
		t.Fatal(err)
	}

	err := newVerifier(nil).Verify(path, nil)
	if err == nil {
		t.Fatal("a world-writable module was accepted")
	}
	// Whichever condition it trips first, it must not be silent about it.
	if err.Error() == "" {
		t.Fatal("refused without saying why")
	}
}

func TestLinuxVerifierRefusesAMissingBinary(t *testing.T) {
	if err := newVerifier(nil).Verify(filepath.Join(t.TempDir(), "absent"), nil); err == nil {
		t.Fatal("a nonexistent module was accepted")
	}
}
