// Command weavemanifest mints and verifies the channel-manifest signing chain:
// an offline root key endorses named signing keys; signing keys sign channel
// manifests.
//
// It lives in core rather than in the channels repository so that there is
// exactly one verifier: verify calls internal/manifestverify, the same code
// core runs before it trusts a fetched manifest. A tool that re-implemented
// the chain could drift from the agent, and the drift would be a security
// bug that every green build hid.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/deploymenttheory/weaveplatform-agent-core/internal/manifestverify"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/manifest"
)

const usage = `weavemanifest — channel manifest tooling

Usage:
  weavemanifest keygen <key-id> <out-prefix>
      Generate an Ed25519 keypair: <out-prefix>.pub (JSON public key) and
      <out-prefix>.key (base64 private key — guard it; the root key stays
      offline).

  weavemanifest endorse <root.key> <signing.pub>
      Root-endorse a signing key: writes <signing.pub>.sig.

  weavemanifest sign <signing.key> <file>
      Sign a file with a signing key: writes <file>.sig.

  weavemanifest verify <root.pub> <signing.pub> <file>
      Verify the full chain exactly as core does: root endorsement of the
      signing key, then the signing key's signature over the file, then the
      manifest parses. Exits non-zero on any break.
`

func main() {
	if len(os.Args) < 2 {
		fail(usage)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "endorse":
		err = signFile(os.Args[2:], true)
	case "sign":
		err = signFile(os.Args[2:], false)
	case "verify":
		err = verify(os.Args[2:])
	case "generate", "promote", "pin":
		err = fmt.Errorf("%s: assembled by CI in the module publish pipeline; not yet a local verb", os.Args[1])
	default:
		fail(usage)
	}
	if err != nil {
		fail("weavemanifest: %v\n", err)
	}
}

// privateKeyFile is the one document the sdk does not define: private keys
// never travel, so the format is this tool's alone.
type privateKeyFile struct {
	Schema     int    `json:"schema"`
	KeyID      string `json:"key_id"`
	PrivateKey string `json:"private_key"`
}

func keygen(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("keygen <key-id> <out-prefix>")
	}
	id, prefix := args[0], args[1]
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := writeJSON(prefix+".pub", 0o644, manifest.PublicKey{
		Schema: 1, KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(pub),
	}); err != nil {
		return err
	}
	if err := writeJSON(prefix+".key", 0o600, privateKeyFile{
		Schema: 1, KeyID: id, PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}); err != nil {
		return err
	}
	fmt.Printf("generated %s.pub and %s.key (key id %q)\n", prefix, prefix, id)
	return nil
}

// signFile signs args[1] with the private key at args[0]. endorse only
// differs in the domain-separation context: an endorsement can never be
// replayed as a manifest signature or vice versa.
func signFile(args []string, endorse bool) error {
	if len(args) != 2 {
		if endorse {
			return fmt.Errorf("endorse <root.key> <signing.pub>")
		}
		return fmt.Errorf("sign <signing.key> <file>")
	}
	priv, keyID, err := readPrivate(args[0])
	if err != nil {
		return err
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	context := manifest.ManifestContext
	if endorse {
		context = manifest.EndorseContext
	}
	sig := ed25519.Sign(priv, manifest.SigningMessage(context, data))
	out := args[1] + ".sig"
	if err := writeJSON(out, 0o644, manifest.Signature{
		Schema: 1, KeyID: keyID, Signature: base64.StdEncoding.EncodeToString(sig),
	}); err != nil {
		return err
	}
	fmt.Printf("signed %s → %s (key %q)\n", args[1], out, keyID)
	return nil
}

// verify hands the four files to core's verifier. Reading them here and
// deciding nothing is the point: if this passes, core passes.
func verify(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("verify <root.pub> <signing.pub> <file>")
	}
	rootData, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	_, rootRaw, err := manifest.ParsePublicKey(rootData)
	if err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	var b manifestverify.Bundle
	for _, f := range []struct {
		path string
		dst  *[]byte
	}{
		{args[1], &b.SigningKey},
		{args[1] + ".sig", &b.SigningKeySig},
		{args[2], &b.Manifest},
		{args[2] + ".sig", &b.ManifestSig},
	} {
		if *f.dst, err = os.ReadFile(f.path); err != nil {
			return err
		}
	}
	m, err := manifestverify.Verify(ed25519.PublicKey(rootRaw), b)
	if err != nil {
		return err
	}
	fmt.Printf("OK: %s (channel %q, sequence %d) verifies under root\n", args[2], m.Channel, m.Sequence)
	return nil
}

func readPrivate(path string) (ed25519.PrivateKey, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var pk privateKeyFile
	if err := json.Unmarshal(data, &pk); err != nil {
		return nil, "", err
	}
	raw, err := base64.StdEncoding.DecodeString(pk.PrivateKey)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, "", fmt.Errorf("%s: not a valid private key", path)
	}
	return ed25519.PrivateKey(raw), pk.KeyID, nil
}

func writeJSON(path string, mode os.FileMode, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), mode)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}
