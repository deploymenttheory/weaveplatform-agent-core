package manifest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// The signing chain is two-tier, minisign-shaped: an offline root key
// endorses named signing keys; signing keys sign channel manifests. All
// signatures are detached Ed25519 over the exact file bytes, stored
// beside the file as <name>.sig in this JSON format. Verification logic
// lives in core (weaveplatform-agent/internal/manifestverify) — a CVE
// there is a core patch, not an SDK rebuild; these are only the format
// types.

// PublicKey is a stored public key (<name>.pub).
type PublicKey struct {
	Schema int    `json:"schema"`
	KeyID  string `json:"key_id"`
	// Ed25519 public key, base64.
	PublicKey string `json:"public_key"`
}

// Signature is a detached signature (<name>.sig).
type Signature struct {
	Schema int    `json:"schema"`
	KeyID  string `json:"key_id"`
	// Ed25519 signature over the signed file's exact bytes, base64.
	Signature string `json:"signature"`
}

// ParsePublicKey decodes a stored public key and returns its raw bytes.
func ParsePublicKey(data []byte) (*PublicKey, []byte, error) {
	var pk PublicKey
	if err := json.Unmarshal(data, &pk); err != nil {
		return nil, nil, fmt.Errorf("public key: %w", err)
	}
	if pk.Schema != 1 || pk.KeyID == "" {
		return nil, nil, fmt.Errorf("public key: bad schema or key id")
	}
	raw, err := base64.StdEncoding.DecodeString(pk.PublicKey)
	if err != nil || len(raw) != 32 {
		return nil, nil, fmt.Errorf("public key: not a valid ed25519 key")
	}
	return &pk, raw, nil
}

// ParseSignature decodes a detached signature and returns its raw bytes.
func ParseSignature(data []byte) (*Signature, []byte, error) {
	var sig Signature
	if err := json.Unmarshal(data, &sig); err != nil {
		return nil, nil, fmt.Errorf("signature: %w", err)
	}
	if sig.Schema != 1 || sig.KeyID == "" {
		return nil, nil, fmt.Errorf("signature: bad schema or key id")
	}
	raw, err := base64.StdEncoding.DecodeString(sig.Signature)
	if err != nil || len(raw) != 64 {
		return nil, nil, fmt.Errorf("signature: not a valid ed25519 signature")
	}
	return &sig, raw, nil
}
