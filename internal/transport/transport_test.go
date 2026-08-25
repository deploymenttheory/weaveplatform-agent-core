package transport

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	agentv1 "github.com/deploymenttheory/weaveplatform-agent/sdk/gen/go/weave/agent/v1"
	"github.com/deploymenttheory/weaveplatform-agent/test/stubgateweave"
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
		if _, err := m1.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE, "k", []byte(d), true); err != nil {
			t.Fatal(err)
		}
	}
	if keys, _ := queue.List(context.Background(), "core.transport", "queue/"); len(keys) != 2 {
		t.Fatalf("after enqueue: %d queued, want 2", len(keys))
	}

	// "Restart": a fresh Mux over the same store queues a third message.
	// If nextID reset to 1 it would overwrite message "a".
	m2 := &Mux{Log: log, Queue: queue}
	if _, err := m2.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE, "k", []byte("c"), true); err != nil {
		t.Fatal(err)
	}
	keys, _ := queue.List(context.Background(), "core.transport", "queue/")
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
	delivered, err := mux.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("a"), true)
	if err != nil || !delivered {
		t.Fatalf("send: delivered=%v err=%v", delivered, err)
	}

	// Peer down: queueOffline queues instead of failing.
	srv.Close()
	delivered, err = mux.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("b"), true)
	if err != nil || delivered {
		t.Fatalf("offline send: delivered=%v err=%v", delivered, err)
	}
	// Without queueOffline the failure is reported.
	if _, err := mux.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("c"), false); err == nil {
		t.Fatal("offline send without queueing reported success")
	}
	if keys, _ := queue.List(context.Background(), "core.transport", "queue/"); len(keys) != 1 {
		t.Fatalf("queue length = %d, want 1", len(keys))
	}

	// Peer returns (same handler, new listener): the next successful send
	// flushes the queue.
	srv2 := httptest.NewServer(stub.Handler())
	defer srv2.Close()
	peer.URL = srv2.URL + "/v1/messages"
	delivered, err = mux.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE, "heartbeat", []byte("d"), true)
	if err != nil || !delivered {
		t.Fatalf("recovered send: delivered=%v err=%v", delivered, err)
	}
	msgs := stub.Messages()
	// a (first), d (recovery), b (flushed). c was refused, never queued.
	if len(msgs) != 3 {
		t.Fatalf("stub received %d messages, want 3: %+v", len(msgs), msgs)
	}
	if keys, _ := queue.List(context.Background(), "core.transport", "queue/"); len(keys) != 0 {
		t.Fatalf("queue not drained: %v", keys)
	}
}

// TestConcurrentOfflineSendsNoLossNoDup fires many offline sends at once with
// the peer down: the monotonic queue key must not race (no overwrite, no
// loss), and when the peer returns every message flushes exactly once (no
// double delivery under the flush mutex).
func TestConcurrentOfflineSendsNoLossNoDup(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	queue := hostserv.NewMemStore()
	// No peer wired yet: every send with queueOffline lands in the queue.
	mux := &Mux{Log: log, Queue: queue}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = mux.Send(context.Background(), "sysinfo", agentv1.Peer_PEER_GATEWEAVE,
				"heartbeat", []byte{byte(i)}, true)
		}(i)
	}
	wg.Wait()

	keys, _ := queue.List(context.Background(), "core.transport", "queue/")
	if len(keys) != n {
		t.Fatalf("queued %d messages, want %d (a racing key overwrote or dropped one)", len(keys), n)
	}

	// Bring a peer up and flush; the stub must receive each message once.
	stub := stubgateweave.New()
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()
	mux.GateWeave = &HTTPPeer{URL: srv.URL + "/v1/messages", Device: func() string { return "device-1" }}
	mux.Flush(agentv1.Peer_PEER_GATEWEAVE)

	if got := len(stub.Messages()); got != n {
		t.Fatalf("stub received %d messages, want %d (loss or double delivery)", got, n)
	}
	if keys, _ := queue.List(context.Background(), "core.transport", "queue/"); len(keys) != 0 {
		t.Fatalf("queue not drained after flush: %d left", len(keys))
	}
}
