// Package stubgateweave is the stand-in server core develops against
// until GateWeave exists: enrolment, policy delivery, and artifact
// serving, just enough surface for core's clients to be real.
package stubgateweave

import (
	"crypto/rand"
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
	enrolled map[string]string // enrolment token → device id
	// Artifacts served under /artifacts/<name>.
	artifacts map[string][]byte
	// Messages received on /v1/messages.
	messages []ReceivedMessage
}

// ReceivedMessage is one message a device sent.
type ReceivedMessage struct {
	DeviceID string `json:"device_id"`
	Module   string `json:"module"`
	Kind     string `json:"kind"`
	Data     []byte `json:"data"`
}

// New returns an empty stub.
func New() *Server {
	return &Server{
		modules:   make(map[string]json.RawMessage),
		enrolled:  make(map[string]string),
		artifacts: make(map[string][]byte),
	}
}

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
	mux.HandleFunc("POST /v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var b [8]byte
		rand.Read(b[:]) //nolint:errcheck
		id := "device-" + hex.EncodeToString(b[:])
		s.mu.Lock()
		s.enrolled[id] = id
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"device_id": id, "tenant": "stub"}) //nolint:errcheck
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
