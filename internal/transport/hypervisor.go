package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-api/hvchannel"
)

// The hypervisor channel is a single byte pipe (virtio-serial / vsock /
// HvSocket) between core inside the guest and the host/CLI. Core owns exactly
// one connection: the frame protocol has NO resynchronisation, so a second
// reader on the same wire desynchronises it permanently rather than degrading.
// One fd, one read loop, one write mutex — always.
//
// The framing and addressing format itself lives in
// weaveplatform-api/hvchannel, because the host end of this wire must encode
// identically and nothing on the channel would detect a mismatch. Core
// translates between hvchannel envelopes and the module-facing (module, kind,
// data) Transport primitives; modules never see framing or device nodes.

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
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if err := hvchannel.WriteEnvelope(p.w, hvchannel.Envelope{Module: module, Kind: kind, Data: data}); err != nil {
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
		env, err := hvchannel.ReadEnvelope(p.r)
		if err != nil {
			// An undecodable envelope costs one message, not the channel: the
			// length prefix has already kept the reader aligned, so carry on.
			// Only a stream-level failure ends the loop.
			if isFrameDecodeError(err) {
				p.log.Warn("hypervisor channel: undecodable frame", "err", err)
				continue
			}
			if err != io.EOF && ctx.Err() == nil {
				p.log.Warn("hypervisor channel read ended", "err", err)
			}
			return
		}
		p.deliver(env.Module, env.Kind, env.Data)
	}
}

// isFrameDecodeError reports whether err is a per-message decode failure (the
// stream is still aligned) rather than a stream failure (it is not). Framing
// errors wrap a *json.SyntaxError or *json.UnmarshalTypeError; I/O errors do
// not.
func isFrameDecodeError(err error) bool {
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typeErr)
}
