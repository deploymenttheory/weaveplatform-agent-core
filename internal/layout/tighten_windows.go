package layout

// tightenDir is a no-op on Windows: directory access is governed by ACLs,
// not Unix mode bits (Go's os.Chmod only toggles the read-only attribute).
// The installer sets a SYSTEM+Administrators ACL on the state root; see
// WINDOWS_HANDOFF.md.
func tightenDir(_ string) error { return nil }
