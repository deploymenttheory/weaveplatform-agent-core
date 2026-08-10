package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
)

// TestHypervisorLoopback drives the whole channel over an in-process pipe: a
// fake host on one end, core's peer + Mux on the other. Proves inbound frames
// reach a module's Receive stream and outbound Send frames reach the host —
// the mechanism the real virtio-serial/vsock/HvSocket devices only change the
// transport under.
func TestHypervisorLoopback(t *testing.T) {
	guestConn, hostConn := net.Pipe()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mux := &Mux{Log: log}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mux.Hypervisor = newHypervisorPeer(ctx, guestConn, log, mux.deliver)

	// A module subscribes to inbound messages.
	in := mux.Receive(ctx, "guestweave")

	hostR := bufio.NewReader(hostConn)
	hostW := bufio.NewWriter(hostConn)

	// --- Inbound: host -> guest module ---
	go func() {
		payload, _ := json.Marshal(hvEnvelope{
			Module: "guestweave", Kind: "guestweave.presence.hello", Data: []byte(`{"id":"h1"}`),
		})
		_ = writeFrame(hostW, payload)
		_ = hostW.Flush()
	}()

	select {
	case msg := <-in:
		if msg.Kind != "guestweave.presence.hello" {
			t.Fatalf("inbound kind = %q", msg.Kind)
		}
		if msg.Peer != agentv1.Peer_PEER_HYPERVISOR {
			t.Fatalf("inbound peer = %v, want hypervisor", msg.Peer)
		}
		if string(msg.Data) != `{"id":"h1"}` {
			t.Fatalf("inbound data = %s", msg.Data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("inbound message never delivered")
	}

	// --- Outbound: guest module -> host ---
	go func() {
		_, _ = mux.Send(ctx, "guestweave", agentv1.Peer_PEER_HYPERVISOR,
			"guestweave.presence.hello.result", []byte(`{"id":"h1","payload":{"os":"linux"}}`), false)
	}()

	done := make(chan hvEnvelope, 1)
	go func() {
		frame, err := readFrame(hostR)
		if err != nil {
			return
		}
		var env hvEnvelope
		_ = json.Unmarshal(frame, &env)
		done <- env
	}()

	select {
	case env := <-done:
		if env.Module != "guestweave" || env.Kind != "guestweave.presence.hello.result" {
			t.Fatalf("outbound envelope = %+v", env)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("outbound frame never reached the host")
	}
}

// TestDeliverNoReceiverDoesNotBlock ensures a message for a module with no
// live Receive stream is dropped (logged), never blocking the read loop.
func TestDeliverNoReceiverDoesNotBlock(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mux := &Mux{Log: log}
	done := make(chan struct{})
	go func() {
		mux.deliver("absent", "k", []byte("x"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deliver blocked with no receiver")
	}
}
