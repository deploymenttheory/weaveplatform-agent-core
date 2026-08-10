// Package stubgateweave is the stand-in server core develops against
// until GateWeave exists: enrolment, policy delivery, and artifact
// serving, just enough surface for core's clients to be real.
package stubgateweave

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
)

// Server is the in-memory stub. Use with httptest.NewServer(s.Handler()).
type Server struct {
	mu       sync.Mutex
	revision uint64
	modules  map[string]json.RawMessage
	enrolled map[string]string // device id → device id
	// Artifacts served under /artifacts/<name>.
	artifacts map[string][]byte
	// Messages received on /v1/messages.
	messages []ReceivedMessage

	// enrolment signing key; the client pins EnrollPub().
	enrollPriv ed25519.PrivateKey
	enrollPub  ed25519.PublicKey
	// outstanding challenge nonces keyed by public_key.
	challenges map[string][]byte
}

// bytesEqual is a tiny constant-length equality (avoids importing bytes
// just for this).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// ReceivedMessage is one message a device sent.
type ReceivedMessage struct {
	DeviceID string `json:"device_id"`
	Module   string `json:"module"`
	Kind     string `json:"kind"`
	Data     []byte `json:"data"`
}

// New returns an empty stub with a fresh enrolment signing key.
func New() *Server {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader) //nolint:errcheck
	return &Server{
		modules:    make(map[string]json.RawMessage),
		enrolled:   make(map[string]string),
		artifacts:  make(map[string][]byte),
		challenges: make(map[string][]byte),
		enrollPriv: priv,
		enrollPub:  pub,
	}
}

// EnrollPub is the server's enrolment public key; a client pins this to
// verify the signed device_id/tenant assignment.
func (s *Server) EnrollPub() ed25519.PublicKey { return s.enrollPub }

// SetPolicy replaces one module's policy document and bumps the revision.
func (s *Server) SetPolicy(module string, doc []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	s.modules[module] = json.RawMessage(doc)
}

// SetArtifact registers a downloadable artifact.
func (s *Server) SetArtifact(name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts[name] = data
}

// Messages returns everything received on /v1/messages.
func (s *Server) Messages() []ReceivedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ReceivedMessage(nil), s.messages...)
}

// Handler returns the HTTP surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/policy", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"revision": s.revision,
			"modules":  s.modules,
		})
	})
	mux.HandleFunc("GET /v1/enroll/challenge", func(w http.ResponseWriter, r *http.Request) {
		pubKey := r.URL.Query().Get("public_key")
		if pubKey == "" {
			http.Error(w, "missing public_key", http.StatusBadRequest)
			return
		}
		var nonce [32]byte
		rand.Read(nonce[:]) //nolint:errcheck
		s.mu.Lock()
		s.challenges[pubKey] = nonce[:]
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"nonce": base64.StdEncoding.EncodeToString(nonce[:]),
		})
	})
	mux.HandleFunc("POST /v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PublicKey string `json:"public_key"`
			Nonce     string `json:"nonce"`
			NonceSig  string `json:"nonce_sig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			http.Error(w, "bad public_key", http.StatusBadRequest)
			return
		}
		nonce, _ := base64.StdEncoding.DecodeString(req.Nonce)  //nolint:errcheck
		sig, _ := base64.StdEncoding.DecodeString(req.NonceSig) //nolint:errcheck

		// Proof of possession: the device must have signed the nonce we
		// issued for this public key.
		s.mu.Lock()
		want := s.challenges[req.PublicKey]
		delete(s.challenges, req.PublicKey)
		s.mu.Unlock()
		if len(want) == 0 || !bytesEqual(want, nonce) || !ed25519.Verify(pub, nonce, sig) {
			http.Error(w, "challenge verification failed", http.StatusUnauthorized)
			return
		}

		var b [8]byte
		rand.Read(b[:]) //nolint:errcheck
		id := "device-" + hex.EncodeToString(b[:])
		s.mu.Lock()
		s.enrolled[id] = id
		s.mu.Unlock()

		// Sign the assignment so the client can verify origin.
		assignment, _ := json.Marshal(map[string]string{"device_id": id, "tenant": "stub"}) //nolint:errcheck
		sigAssign := ed25519.Sign(s.enrollPriv, assignment)
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"device_id":      id,
			"tenant":         "stub",
			"assignment":     base64.StdEncoding.EncodeToString(assignment),
			"assignment_sig": base64.StdEncoding.EncodeToString(sigAssign),
		})
	})
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var msg ReceivedMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.messages = append(s.messages, msg)
		s.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /artifacts/{name}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		data, ok := s.artifacts[r.PathValue("name")]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data) //nolint:errcheck
	})
	return mux
}
