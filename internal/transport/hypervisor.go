package transport

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
)

// The hypervisor channel is a single byte pipe (virtio-serial / vsock /
// HvSocket) between core inside the guest and the host/CLI. Core owns exactly
// one connection: the frame protocol has NO resynchronisation, so a second
// reader on the same wire desynchronises it permanently rather than degrading.
// One fd, one read loop, one write mutex — always.
//
// The wire is length-prefixed frames (4-byte big-endian length + payload),
// each payload a JSON hvEnvelope addressing a module. Core translates between
// envelopes and the module-facing (module, kind, data) Transport primitives;
// modules never see framing or device nodes.

// maxFrameSize bounds a single frame, matching the legacy agent (512 MiB), so
// a corrupt length can't drive an unbounded allocation.
const maxFrameSize = 512 << 20

// hvEnvelope is the on-wire addressing header: which module a message is for,
// the operation kind, and the opaque payload. Peer is implicit (hypervisor).
type hvEnvelope struct {
	Module string `json:"module"`
	Kind   string `json:"kind"`
	Data   []byte `json:"data,omitempty"`
}

// HypervisorPeer implements Peer over a single owned connection.
type HypervisorPeer struct {
	log     *slog.Logger
	deliver func(module, kind string, data []byte)

	conn io.Closer
	r    *bufio.Reader
	wmu  sync.Mutex // single writer
	w    *bufio.Writer
}

// ConnectHypervisor opens the probed hypervisor device and wires it as the
// PEER_HYPERVISOR peer, starting the single read loop on ctx. Call once at
// startup when the hypervisor.channel capability is present. attrs is that
// capability's probe attribute map (device path etc.).
func (m *Mux) ConnectHypervisor(ctx context.Context, attrs map[string]string) error {
	rwc, err := openDevice(attrs)
	if err != nil {
		return fmt.Errorf("transport: opening hypervisor channel: %w", err)
	}
	m.Hypervisor = newHypervisorPeer(ctx, rwc, m.Log, m.deliver)
	// Drain anything queued while the channel was down.
	m.Flush(agentv1.Peer_PEER_HYPERVISOR)
	return nil
}

// newHypervisorPeer wraps an already-open connection and starts the single
// read loop. Exposed for the loopback test; production goes via
// Mux.ConnectHypervisor.
func newHypervisorPeer(ctx context.Context, rwc io.ReadWriteCloser, log *slog.Logger, deliver func(module, kind string, data []byte)) *HypervisorPeer {
	p := &HypervisorPeer{
		log:     log,
		deliver: deliver,
		conn:    rwc,
		r:       bufio.NewReader(rwc),
		w:       bufio.NewWriter(rwc),
	}
	go p.readLoop(ctx)
	return p
}

// Send implements Peer: one addressed frame, written under the single-writer
// mutex.
func (p *HypervisorPeer) Send(_ context.Context, module, kind string, data []byte) error {
	payload, err := json.Marshal(hvEnvelope{Module: module, Kind: kind, Data: data})
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if err := writeFrame(p.w, payload); err != nil {
		return err
	}
	return p.w.Flush()
}

// readLoop is the sole reader of the wire. It decodes each frame and fans it
// out to the addressed module's receivers via deliver, until the connection
// errors or ctx ends.
func (p *HypervisorPeer) readLoop(ctx context.Context) {
	defer p.conn.Close() //nolint:errcheck
	for {
		if ctx.Err() != nil {
			return
		}
		payload, err := readFrame(p.r)
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				p.log.Warn("hypervisor channel read ended", "err", err)
			}
			return
		}
		var env hvEnvelope
		if err := json.Unmarshal(payload, &env); err != nil {
			p.log.Warn("hypervisor channel: undecodable frame", "err", err)
			continue // one bad frame is not fatal; the length prefix keeps us aligned
		}
		p.deliver(env.Module, env.Kind, env.Data)
	}
}

// writeFrame writes a length-prefixed frame.
func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrameSize {
		return fmt.Errorf("transport: frame too large: %d", len(payload))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one length-prefixed frame.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameSize {
		return nil, fmt.Errorf("transport: frame length %d exceeds max", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
