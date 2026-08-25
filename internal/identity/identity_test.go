package identity

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/weaveplatform-agent-core/internal/hostserv"
	"github.com/deploymenttheory/weaveplatform-agent-core/test/stubgateweave"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestEnrolmentAndPersistence(t *testing.T) {
	stub := stubgateweave.New()
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()
	store := hostserv.NewMemStore()

	p := &Provider{
		Log: testLog(), Store: store,
		EnrollURL:           srv.URL + "/v1/enroll",
		ServerPub:           stub.EnrollPub(),
		AllowInsecureEnroll: true, // httptest is http, not https
	}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}
	id, eph, _ := p.WhoAmI(context.Background())
	if !eph || !strings.HasPrefix(id, "ephemeral-") {
		t.Fatalf("pre-enrolment identity: %s eph=%v", id, eph)
	}
	if err := p.Enroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	id, eph, tenant := p.WhoAmI(context.Background())
	if eph || !strings.HasPrefix(id, "device-") || tenant != "stub" {
		t.Fatalf("post-enrolment identity: %s eph=%v tenant=%s", id, eph, tenant)
	}
	if !p.Enrolled() {
		t.Fatal("Enrolled() false after enrolment")
	}

	// A fresh provider over the same store loads the identity — enrolment
	// survives restart, and Enroll is a no-op.
	p2 := &Provider{
		Log: testLog(), Store: store,
		EnrollURL:           srv.URL + "/v1/enroll",
		ServerPub:           stub.EnrollPub(),
		AllowInsecureEnroll: true,
	}
	if err := p2.Init(); err != nil {
		t.Fatal(err)
	}
	id2, eph2, _ := p2.WhoAmI(context.Background())
	if eph2 || id2 != id {
		t.Fatalf("identity lost across restart: %s vs %s", id2, id)
	}
	if err := p2.Enroll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if id3, _, _ := p2.WhoAmI(context.Background()); id3 != id {
		t.Fatal("re-enrolment changed identity")
	}
}

func TestCredentialFailsClosed(t *testing.T) {
	p := &Provider{Log: testLog(), Store: hostserv.NewMemStore()}
	if err := p.Init(); err != nil {
		t.Fatal(err)
	}
	// Until real, verifiable, issuer-signed semantics exist, Credential
	// must fail closed rather than mint a forgeable token that echoes the
	// requested scopes as granted.
	token, _, granted, err := p.Credential(context.Background(), "sysinfo", []string{"telemetry:write"})
	if err == nil {
		t.Fatalf("Credential returned a token (%q, granted=%v); it must fail closed", token, granted)
	}
}
