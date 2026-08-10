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
func (p *Provider) Enroll(ctx context.Context) error {
	p.mu.Lock()
	if p.deviceID != "" || p.EnrollURL == "" {
		p.mu.Unlock()
		return nil
	}
	pub := p.priv.Public().(ed25519.PublicKey)
	p.mu.Unlock()

	body, err := json.Marshal(map[string]string{
		"public_key": base64.StdEncoding.EncodeToString(pub),
	})
	if err != nil {
		return err
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
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
		DeviceID string `json:"device_id"`
		Tenant   string `json:"tenant"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.DeviceID == "" {
		return fmt.Errorf("identity: malformed enrolment response")
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

// WhoAmI implements hostserv.IdentityBackend.
func (p *Provider) WhoAmI() (string, bool, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.deviceID != "" {
		return p.deviceID, false, p.tenant
	}
	return p.ephemeral, true, ""
}

// Credential implements hostserv.IdentityBackend: an opaque token scoped
// to the module. Until GateWeave defines token semantics this is a
// core-minted random credential — the shape modules program against is
// what matters.
func (p *Provider) Credential(module string, scopes []string) (string, int64, []string, error) {
	var b [16]byte
	rand.Read(b[:]) //nolint:errcheck
	deviceID, _, _ := p.WhoAmI()
	token := "wv1." + deviceID + "." + module + "." + hex.EncodeToString(b[:])
	return token, time.Now().Add(time.Hour).Unix(), scopes, nil
}

// Enrolled reports enrolment state for the control surface.
func (p *Provider) Enrolled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deviceID != ""
}
