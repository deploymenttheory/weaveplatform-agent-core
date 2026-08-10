package keyprotect

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// TestDPAPIRoundTrip is a Windows-host handoff test: seal then unseal a key
// and confirm it survives, and that the sealed blob is not the plaintext.
// Run on Windows: `go test ./internal/store/keyprotect/`.
func TestDPAPIRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	p := New()
	sealed, err := p.Seal(key)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, key) {
		t.Fatal("sealed blob contains the plaintext key")
	}
	got, err := p.Unseal(sealed)
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatal("unsealed key differs from original")
	}
}

// TestDPAPIEntropyBinding confirms that a blob sealed with the machine
// entropy cannot be unsealed without it — i.e. entropy is actually applied.
// If this fails, the secondary-entropy binding regressed to machine-scope
// only (any local process could unseal).
func TestDPAPIEntropyBinding(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key) //nolint:errcheck
	sealed, err := New().Seal(key)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Unseal with WRONG entropy must fail. We reach past the public API to
	// prove entropy is in play.
	_, err = dpapi(sealed, []byte("not-the-machine-guid"), unsealOp)
	if err == nil {
		t.Fatal("unseal succeeded with wrong entropy; secondary entropy is not applied")
	}
}
