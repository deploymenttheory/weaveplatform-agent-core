// Package identity owns who this device is: the keypair, enrolment, and
// per-module credential scoping. The Provider interface is the seam
// hardware backing (Secure Enclave, TPM) slots into; v1 is a software
// Ed25519 key persisted in the encrypted store.
package identity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
)

// storeNamespace is the core-owned store namespace for identity state.
// Dotted, so no module id can collide with it.
const storeNamespace = "core.identity"

// Provider is the identity seam. Software today; hardware-backed
// implementations replace the key handling, not the enrolment.
type Provider struct {
	Log *slog.Logger
	// Store persists the keypair and enrolment state, encrypted.
	Store hostserv.StoreBackend
	// EnrollURL is GateWeave's enrolment endpoint; empty stays
	// unenrolled (ephemeral identity).
	EnrollURL string
	// ServerPub is the pinned Ed25519 public key the enrolment server
	// signs its device_id/tenant assignment with. Required in release;
	// without it (and without AllowInsecureEnroll) enrolment is refused.
	ServerPub ed25519.PublicKey
	// AllowInsecureEnroll permits http and an unpinned server key. Dev
	// only; core sets it from the dev build tag.
	AllowInsecureEnroll bool
	// Client for enrolment; nil gets a 30s default.
	Client *http.Client

	mu        sync.Mutex
	priv      ed25519.PrivateKey
	deviceID  string
	tenant    string
	ephemeral string
}

type enrolmentState struct {
	DeviceID string `json:"device_id"`
	Tenant   string `json:"tenant"`
	// PrivateKey is the Ed25519 seed+pub, base64. The store encrypts at
	// rest; this field never leaves the store namespace.
	PrivateKey string `json:"private_key"`
}

// Init loads or creates the keypair and prior enrolment. Call once at
// startup, before Enroll.
func (p *Provider) Init() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b [8]byte
	rand.Read(b[:]) //nolint:errcheck
	p.ephemeral = "ephemeral-" + hex.EncodeToString(b[:])

	raw, found, err := p.Store.Get(storeNamespace, "state")
	if err != nil {
		return err
	}
	if found {
		var st enrolmentState
		if err := json.Unmarshal(raw, &st); err == nil {
			if key, err := base64.StdEncoding.DecodeString(st.PrivateKey); err == nil && len(key) == ed25519.PrivateKeySize {
				p.priv = ed25519.PrivateKey(key)
				p.deviceID = st.DeviceID
				p.tenant = st.Tenant
				if p.deviceID != "" {
					p.Log.Info("identity loaded", "device_id", p.deviceID, "tenant", p.tenant)
				}
				return nil
			}
		}
		p.Log.Warn("discarding corrupt identity state")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	p.priv = priv
	return p.saveLocked()
}

func (p *Provider) saveLocked() error {
	st := enrolmentState{
		DeviceID:   p.deviceID,
		Tenant:     p.tenant,
		PrivateKey: base64.StdEncoding.EncodeToString(p.priv),
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return p.Store.Put(storeNamespace, "state", raw)
}

// Enroll registers the device with GateWeave if not already enrolled.
// Idempotent; a no-op when EnrollURL is empty or enrolment exists.
//
// The handshake proves both directions, over TLS:
//  1. GET  <EnrollURL>/challenge?public_key=… → a server nonce.
//  2. POST <EnrollURL> {public_key, nonce, nonce_sig} — nonce_sig is the
//     device key signing the nonce, proving possession of the private key.
//  3. The server replies {device_id, tenant, assignment, assignment_sig}
//     where assignment_sig is the server signing the assignment with its
//     enrolment key. The client verifies it against the pinned ServerPub,
//     so a MITM cannot bind the device into an attacker tenant.
//
// EnrollURL must be https in release builds (AllowInsecureEnroll gates the
// dev exception). ServerPub is required unless AllowInsecureEnroll is set.
func (p *Provider) Enroll(ctx context.Context) error {
	p.mu.Lock()
	if p.deviceID != "" || p.EnrollURL == "" {
		p.mu.Unlock()
		return nil
	}
	pub := p.priv.Public().(ed25519.PublicKey)
	priv := p.priv
	p.mu.Unlock()

	if err := p.checkEnrolTransport(); err != nil {
		return err
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	// 1. Fetch the challenge nonce.
	chalURL := strings.TrimSuffix(p.EnrollURL, "/") + "/challenge?public_key=" + url.QueryEscape(pubB64)
	creq, err := http.NewRequestWithContext(ctx, http.MethodGet, chalURL, nil)
	if err != nil {
		return err
	}
	cresp, err := client.Do(creq)
	if err != nil {
		return fmt.Errorf("identity: enrolment challenge: %w", err)
	}
	defer cresp.Body.Close()
	if cresp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: enrolment challenge returned %s", cresp.Status)
	}
	var chal struct {
		Nonce string `json:"nonce"`
	}
	if err := json.NewDecoder(io.LimitReader(cresp.Body, 1<<20)).Decode(&chal); err != nil || chal.Nonce == "" {
		return fmt.Errorf("identity: malformed enrolment challenge")
	}
	nonceBytes, err := base64.StdEncoding.DecodeString(chal.Nonce)
	if err != nil || len(nonceBytes) < 16 {
		return fmt.Errorf("identity: enrolment nonce invalid")
	}

	// 2. Sign the nonce (proof of possession) and enrol.
	body, err := json.Marshal(map[string]string{
		"public_key": pubB64,
		"nonce":      chal.Nonce,
		"nonce_sig":  base64.StdEncoding.EncodeToString(ed25519.Sign(priv, nonceBytes)),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.EnrollURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("identity: enrolment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("identity: enrolment returned %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var out struct {
		DeviceID      string `json:"device_id"`
		Tenant        string `json:"tenant"`
		Assignment    string `json:"assignment"`     // base64 of the exact signed bytes
		AssignmentSig string `json:"assignment_sig"` // base64 server signature over Assignment
	}
	if err := json.Unmarshal(data, &out); err != nil || out.DeviceID == "" {
		return fmt.Errorf("identity: malformed enrolment response")
	}

	// 3. Verify the server signed this assignment (origin authenticity),
	// and that the signed bytes actually bind this device_id/tenant.
	if err := p.verifyAssignment(out.DeviceID, out.Tenant, out.Assignment, out.AssignmentSig); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.deviceID = out.DeviceID
	p.tenant = out.Tenant
	if err := p.saveLocked(); err != nil {
		return err
	}
	p.Log.Info("device enrolled", "device_id", p.deviceID, "tenant", p.tenant)
	return nil
}

// checkEnrolTransport enforces https unless the dev insecure flag is set.
func (p *Provider) checkEnrolTransport() error {
	u, err := url.Parse(p.EnrollURL)
	if err != nil {
		return fmt.Errorf("identity: bad enrol URL: %w", err)
	}
	if u.Scheme != "https" && !p.AllowInsecureEnroll {
		return fmt.Errorf("identity: enrolment requires https (got %q); set AllowInsecureEnroll only for dev", u.Scheme)
	}
	if len(p.ServerPub) == 0 && !p.AllowInsecureEnroll {
		return fmt.Errorf("identity: no pinned enrolment server key; refusing to enrol")
	}
	return nil
}

// verifyAssignment checks the server's signature over the assignment and
// that the assignment binds the returned device_id/tenant. Skipped only
// when AllowInsecureEnroll is set and no ServerPub is pinned (dev).
func (p *Provider) verifyAssignment(deviceID, tenant, assignmentB64, sigB64 string) error {
	if len(p.ServerPub) == 0 {
		if p.AllowInsecureEnroll {
			p.Log.Warn("DEV: accepting unverified enrolment assignment (no pinned server key)")
			return nil
		}
		return fmt.Errorf("identity: no pinned enrolment server key")
	}
	assignment, err := base64.StdEncoding.DecodeString(assignmentB64)
	if err != nil {
		return fmt.Errorf("identity: assignment not base64")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("identity: assignment signature invalid")
	}
	if !ed25519.Verify(p.ServerPub, assignment, sig) {
		return fmt.Errorf("identity: enrolment assignment not signed by the pinned server key (possible MITM)")
	}
	// The signed bytes must bind exactly what the server told us.
	var signed struct {
		DeviceID string `json:"device_id"`
		Tenant   string `json:"tenant"`
	}
	if err := json.Unmarshal(assignment, &signed); err != nil {
		return fmt.Errorf("identity: signed assignment unparseable")
	}
	if signed.DeviceID != deviceID || signed.Tenant != tenant {
		return fmt.Errorf("identity: signed assignment does not match returned device_id/tenant")
	}
	return nil
}

// WhoAmI implements hostserv.IdentityBackend.
func (p *Provider) WhoAmI() (string, bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.deviceID != "" {
		return p.deviceID, false, p.tenant
	}
	return p.ephemeral, true, ""
}

// Credential implements hostserv.IdentityBackend. It fails closed: until
// GateWeave defines real, verifiable, issuer-signed token semantics (with
// core deciding the granted subset, never echoing the request), minting a
// forgeable token that merely looks scoped and expiring is worse than
// none — it invites something downstream to trust it. Returning an error
// keeps that contract honest.
func (p *Provider) Credential(module string, scopes []string) (string, int64, []string, error) {
	return "", 0, nil, fmt.Errorf("identity: scoped credentials are not yet implemented")
}

// Enrolled reports enrolment state for the control surface.
func (p *Provider) Enrolled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deviceID != ""
}
