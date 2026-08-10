// Package transport is core's channel mux: modules address a peer, core
// routes. The GateWeave peer is an HTTP client (against the stub until
// the real service exists) with an offline queue persisted in the store;
// the hypervisor channel (virtio-serial, HvSocket) is a second peer that
// arrives with the guestweave consolidation — its seam is the Peer
// interface.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-sdk/werror"
)

// queueNamespace is the core-owned store namespace for the offline queue.
const queueNamespace = "core.transport"

// Peer is one channel to somewhere.
type Peer interface {
	// Send delivers one message now or returns an error.
	Send(ctx context.Context, module, kind string, data []byte) error
}

// Mux implements hostserv.TransportBackend over registered peers.
type Mux struct {
	Log *slog.Logger
	// GateWeave peer; nil means unreachable (sends queue or fail).
	GateWeave Peer
	// Hypervisor peer; nil until the hypervisor channel lands.
	Hypervisor Peer
	// Queue persists undeliverable queue_offline messages; nil disables.
	Queue hostserv.StoreBackend
	// MaxQueued bounds the offline queue by message count; 0 gets a
	// default. Over the cap, the oldest queued message is dropped.
	MaxQueued int

	mu      sync.Mutex
	nextID  uint64
	seeded  bool
	flushMu sync.Mutex // serializes flush so concurrent sends don't double-deliver
	dropped uint64
}

const defaultMaxQueued = 10000

func (m *Mux) maxQueued() int {
	if m.MaxQueued > 0 {
		return m.MaxQueued
	}
	return defaultMaxQueued
}

// Dropped returns how many queued messages have been dropped (queue full
// or unparseable), for the control surface.
func (m *Mux) Dropped() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped
}

type queuedMessage struct {
	Module string `json:"module"`
	Peer   int32  `json:"peer"`
	Kind   string `json:"kind"`
	Data   []byte `json:"data"`
}

// Send implements hostserv.TransportBackend.
func (m *Mux) Send(
	ctx context.Context,
	module string,
	peer agentv1.Peer,
	kind string,
	data []byte,
	queueOffline bool,
) (bool, error) {
	p := m.peerFor(peer)
	if p != nil {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := p.Send(sctx, module, kind, data)
		cancel()
		if err == nil {
			m.flush(p, peer)
			return true, nil
		}
		m.Log.Warn("peer send failed", "peer", peer.String(), "err", err)
	}
	if !queueOffline {
		if p == nil {
			return false, fmt.Errorf("peer %s not connected: %w", peer.String(), werror.ErrUnavailable)
		}
		return false, fmt.Errorf("peer %s unreachable: %w", peer.String(), werror.ErrUnavailable)
	}
	m.enqueue(module, peer, kind, data)
	return false, nil
}

// Receive implements hostserv.TransportBackend. Inbound routing arrives
// with the hypervisor channel; today the stream stays open and silent.
func (m *Mux) Receive(ctx context.Context, module string) <-chan *agentv1.TransportMessage {
	ch := make(chan *agentv1.TransportMessage)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

func (m *Mux) peerFor(peer agentv1.Peer) Peer {
	switch peer {
	case agentv1.Peer_PEER_HYPERVISOR:
		return m.Hypervisor
	default:
		return m.GateWeave
	}
}

// seedNextID recovers the queue counter from the store on first use so a
// restart with messages still queued does not reset to 1 and overwrite
// them. Keys are zero-padded, so lexical max is numeric max.
func (m *Mux) seedNextIDLocked() {
	if m.seeded || m.Queue == nil {
		m.seeded = true
		return
	}
	m.seeded = true
	keys, err := m.Queue.List(context.Background(), queueNamespace, "queue/")
	if err != nil {
		return
	}
	for _, k := range keys {
		var n uint64
		if _, err := fmt.Sscanf(k, "queue/%020d", &n); err == nil && n > m.nextID {
			m.nextID = n
		}
	}
}

func (m *Mux) enqueue(module string, peer agentv1.Peer, kind string, data []byte) {
	if m.Queue == nil {
		return
	}
	raw, err := json.Marshal(
		queuedMessage{Module: module, Peer: int32(peer), Kind: kind, Data: data},
	)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.seedNextIDLocked()
	m.nextID++
	key := fmt.Sprintf("queue/%020d", m.nextID)
	m.mu.Unlock()

	if err := m.Queue.Put(context.Background(), queueNamespace, key, raw); err != nil {
		m.Log.Warn("queueing message failed", "err", err)
		return
	}
	m.enforceCap()
	m.Log.Debug("message queued offline", "module", module, "kind", kind)
}

// enforceCap drops the oldest queued messages when over the count cap.
func (m *Mux) enforceCap() {
	keys, err := m.Queue.List(context.Background(), queueNamespace, "queue/")
	if err != nil {
		return
	}
	over := len(keys) - m.maxQueued()
	if over <= 0 {
		return
	}
	sort.Strings(keys) // zero-padded → lexical order is age order.
	for _, k := range keys[:over] {
		m.Queue.Delete(context.Background(), queueNamespace, k) //nolint:errcheck
		m.mu.Lock()
		m.dropped++
		m.mu.Unlock()
	}
	m.Log.Warn("offline queue over cap; dropped oldest", "dropped", over, "cap", m.maxQueued())
}

// flush drains queued messages for a peer after a successful send, in FIFO
// order. Serialized so two concurrent successful sends can't both drain the
// same message (double delivery).
func (m *Mux) flush(p Peer, peer agentv1.Peer) {
	if m.Queue == nil {
		return
	}
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	keys, err := m.Queue.List(context.Background(), queueNamespace, "queue/")
	if err != nil || len(keys) == 0 {
		return
	}
	sort.Strings(keys) // FIFO among multiple queued messages.
	for _, key := range keys {
		raw, found, err := m.Queue.Get(context.Background(), queueNamespace, key)
		if err != nil || !found {
			continue
		}
		var qm queuedMessage
		if err := json.Unmarshal(raw, &qm); err != nil {
			m.Queue.Delete(context.Background(), queueNamespace, key) //nolint:errcheck
			m.mu.Lock()
			m.dropped++
			m.mu.Unlock()
			continue
		}
		if agentv1.Peer(qm.Peer) != peer {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = p.Send(ctx, qm.Module, qm.Kind, qm.Data)
		cancel()
		if err != nil {
			return // peer went away again; keep the rest queued
		}
		m.Queue.Delete(context.Background(), queueNamespace, key) //nolint:errcheck
	}
}

// Flush attempts to drain the queue to a peer that has become reachable.
// Call when a peer connects. No-op if the peer is nil.
func (m *Mux) Flush(peer agentv1.Peer) {
	if p := m.peerFor(peer); p != nil {
		m.flush(p, peer)
	}
}

// HTTPPeer posts messages to a GateWeave-shaped endpoint.
type HTTPPeer struct {
	// URL receives POSTs, e.g. <stub>/v1/messages.
	URL string
	// Device supplies the sender identity per message.
	Device func() string
	// Client; nil gets a 30s default.
	Client *http.Client
}

// Send implements Peer.
func (h *HTTPPeer) Send(ctx context.Context, module, kind string, data []byte) error {
	body, err := json.Marshal(map[string]any{
		"device_id": h.Device(),
		"module":    module,
		"kind":      kind,
		"data":      data,
	})
	if err != nil {
		return err
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("message endpoint returned %s", resp.Status)
	}
	return nil
}
