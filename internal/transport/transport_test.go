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
