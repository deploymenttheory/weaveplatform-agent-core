package transport

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent/test/stubgateweave"
	agentv1 "github.com/deploymenttheory/weaveplatform-api/gen/go/weave/agent/v1"
)

// TestQueueSurvivesRestart proves the offline queue counter is recovered
// from the store on a new Mux, so a restart does not reset nextID to 1 and
// overwrite still-queued messages.
func TestQueueSurvivesRestart(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	queue := hostserv.NewMemStore()

	// First Mux, peer down: queue two messages.
	m1 := &Mux{Log: log, Queue: queue}
	for _, d := range []string{"a", "b"} {
		if _, err := m1.Send("sysinfo", agentv1.Peer_PEER_GATEWEAVE, "k", []byte(d), true); err != nil {
			t.Fatal(err)
		}
	}
	if keys, _ := queue.List("core.transport", "queue/"); len(keys) != 2 {
		t.Fatalf("after enqueue: %d queued, want 2", len(keys))
	}

	// "Restart": a fresh Mux over the same store queues a third message.
	// If nextID reset to 1 it would overwrite message "a".
	m2 := &Mux{Log: log, Queue: queue}
	if _, err := m2.Send("sysinfo", agentv1.Peer_PEER_GATEWEAVE, "k", []byte("c"), true); err != nil {
		t.Fatal(err)
	}
	keys, _ := queue.List("core.transport", "queue/")
	if len(keys) != 3 {
		t.Fatalf("after restart enqueue: %d queued, want 3 (a message was overwritten)", len(keys))
	}
}

func TestSendQueueAndFlush(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	stub := stubgateweave.New()
	srv := httptest.NewServer(stub.Handler())
	peer := &HTTPPeer{URL: srv.URL + "/v1/messages", Device: func() string { return "device-1" }}
	queue := hostserv.NewMemStore()
	mux := &Mux{Log: log, GateWeave: peer, Queue: queue}

	// Delivered while the peer is up.
	delivered, err := mux.Send("sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("a"), true)
	if err != nil || !delivered {
		t.Fatalf("send: delivered=%v err=%v", delivered, err)
	}

	// Peer down: queueOffline queues instead of failing.
	srv.Close()
	delivered, err = mux.Send("sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("b"), true)
	if err != nil || delivered {
		t.Fatalf("offline send: delivered=%v err=%v", delivered, err)
	}
	// Without queueOffline the failure is reported.
	if _, err := mux.Send("sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("c"), false); err == nil {
		t.Fatal("offline send without queueing reported success")
	}
	if keys, _ := queue.List("core.transport", "queue/"); len(keys) != 1 {
		t.Fatalf("queue length = %d, want 1", len(keys))
	}

	// Peer returns (same handler, new listener): the next successful send
	// flushes the queue.
	srv2 := httptest.NewServer(stub.Handler())
	defer srv2.Close()
	peer.URL = srv2.URL + "/v1/messages"
	delivered, err = mux.Send("sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("d"), true)
	if err != nil || !delivered {
		t.Fatalf("recovered send: delivered=%v err=%v", delivered, err)
	}
	msgs := stub.Messages()
	// a (first), d (recovery), b (flushed). c was refused, never queued.
	if len(msgs) != 3 {
		t.Fatalf("stub received %d messages, want 3: %+v", len(msgs), msgs)
	}
	if keys, _ := queue.List("core.transport", "queue/"); len(keys) != 0 {
		t.Fatalf("queue not drained: %v", keys)
	}
}
