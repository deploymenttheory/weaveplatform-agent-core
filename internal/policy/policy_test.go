package policy

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/store"
	"github.com/deploymenttheory/weaveplatform-agent/internal/store/keyprotect"
	"github.com/deploymenttheory/weaveplatform-agent/test/stubgateweave"
)

func TestFetchWatchAndCache(t *testing.T) {
	stub := stubgateweave.New()
	stub.SetPolicy("sysinfo", []byte(`{"interval_seconds": 60}`))
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()

	dir := t.TempDir()
	st, err := store.Open(dir, keyprotect.New())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	m := &Manager{
		Log:      log,
		URL:      srv.URL + "/v1/policy",
		Interval: 50 * time.Millisecond,
		Cache:    st,
	}
	m.Load()
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)

	// First fetch arrives.
	waitFor(t, func() bool {
		_, doc, _ := m.Get(context.Background(), "sysinfo")
		return doc != nil
	})

	// A change on the stub reaches a watcher.
	watch := m.Watch(ctx, "sysinfo")
	stub.SetPolicy("sysinfo", []byte(`{"interval_seconds": 5}`))
	select {
	case <-watch:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher never woke on policy change")
	}
	rev, doc, _ := m.Get(context.Background(), "sysinfo")
	if got := intervalOf(t, doc); got != 5 {
		t.Fatalf("interval = %d, doc = %s (rev %d)", got, doc, rev)
	}

	cancel()
	st.Close()

	// A fresh manager with no URL serves the cached document — offline
	// restart keeps last-known policy.
	st2, err := store.Open(dir, keyprotect.New())
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	m2 := &Manager{Log: log, Cache: st2}
	m2.Load()
	_, doc2, _ := m2.Get(context.Background(), "sysinfo")
	if intervalOf(t, doc2) != 5 {
		t.Fatalf("cache miss after restart: %s", doc2)
	}
}

// TestRevisionRegressionAccepted proves a GateWeave reset (revisions going
// backwards, e.g. restored from backup) still delivers new documents,
// rather than bricking policy delivery forever as the old
// "refuse revision <= current" rule did.
func TestRevisionRegressionAccepted(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := &Manager{Log: log}

	m.apply(Document{Revision: 10, Modules: map[string]json.RawMessage{
		"sysinfo": json.RawMessage(`{"interval_seconds":60}`),
	}}, false)
	if _, doc, _ := m.Get(context.Background(), "sysinfo"); intervalOf(t, doc) != 60 {
		t.Fatalf("first apply not stored")
	}

	// Server resets to a LOWER revision with a different document.
	m.apply(Document{Revision: 1, Modules: map[string]json.RawMessage{
		"sysinfo": json.RawMessage(`{"interval_seconds":5}`),
	}}, false)
	if _, doc, _ := m.Get(context.Background(), "sysinfo"); intervalOf(t, doc) != 5 {
		t.Fatalf("revision regression ignored: policy delivery bricked")
	}

	// Same revision, same content: no-op (no spurious change).
	m.apply(Document{Revision: 1, Modules: map[string]json.RawMessage{
		"sysinfo": json.RawMessage(`{"interval_seconds":5}`),
	}}, false)
	if _, doc, _ := m.Get(context.Background(), "sysinfo"); intervalOf(t, doc) != 5 {
		t.Fatalf("idempotent re-apply changed the doc")
	}
}

func intervalOf(t *testing.T, doc []byte) int {
	t.Helper()
	var p struct {
		IntervalSeconds int `json:"interval_seconds"`
	}
	if err := json.Unmarshal(doc, &p); err != nil {
		t.Fatalf("unmarshal %q: %v", doc, err)
	}
	return p.IntervalSeconds
}

func waitFor(t *testing.T, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}
