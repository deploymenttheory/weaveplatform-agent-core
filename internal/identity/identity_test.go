package identity

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/weaveplatform-agent/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent/test/stubgateweave"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestEnrolmentAndPersistence(t *testing.T) {
	stub := stubgateweave.New()
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()
	store := hostserv.NewMemStore()

	p := &Provider{Log: testLog(), Store: store, EnrollURL: srv.URL + "/v1/enroll"}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}
	id, eph, _ := p.WhoAmI()
	if !eph || !strings.HasPrefix(id, "ephemeral-") {
		t.Fatalf("pre-enrolment identity: %s eph=%v", id, eph)
	}
	if err := p.Enroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, eph, tenant := p.WhoAmI()
	if eph || !strings.HasPrefix(id, "device-") || tenant != "stub" {
		t.Fatalf("post-enrolment identity: %s eph=%v tenant=%s", id, eph, tenant)
	}
	if !p.Enrolled() {
		t.Fatal("Enrolled() false after enrolment")
	}

	// A fresh provider over the same store loads the identity — enrolment
	// survives restart, and Enroll is a no-op.
	p2 := &Provider{Log: testLog(), Store: store, EnrollURL: srv.URL + "/v1/enroll"}
	if err := p2.Init(); err != nil {
		t.Fatal(err)
	}
	id2, eph2, _ := p2.WhoAmI()
	if eph2 || id2 != id {
		t.Fatalf("identity lost across restart: %s vs %s", id2, id)
	}
	if err := p2.Enroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if id3, _, _ := p2.WhoAmI(); id3 != id {
		t.Fatal("re-enrolment changed identity")
	}
}

func TestCredentialScopedToModule(t *testing.T) {
	p := &Provider{Log: testLog(), Store: hostserv.NewMemStore()}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}
	token, exp, granted, err := p.Credential("sysinfo", []string{"telemetry:write"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(token, ".sysinfo.") {
		t.Errorf("token %q not scoped to module", token)
	}
	if exp == 0 || len(granted) != 1 {
		t.Errorf("exp=%d granted=%v", exp, granted)
	}
}
