package handshake

import "testing"

func TestLineRoundTrip(t *testing.T) {
	l := Line{Protocol: 3, Network: "unix", Addr: "/var/run/weave/sysinfo.sock"}
	got, err := Parse(l.Format())
	if err != nil {
		t.Fatal(err)
	}
	if got != l {
		t.Fatalf("round trip: got %+v want %+v", got, l)
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"",
		"WEAVE|1|1|unix",
		"NOPE|1|1|unix|/a",
		"WEAVE|2|1|unix|/a",
		"WEAVE|1|0|unix|/a",
		"WEAVE|1|1|tcp|127.0.0.1:1",
		"WEAVE|1|1|unix|",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted, want error", s)
		}
	}
}

func TestWindow(t *testing.T) {
	w, err := ParseWindow("2", "3")
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[uint32]bool{1: false, 2: true, 3: true, 4: false} {
		if w.Contains(p) != want {
			t.Errorf("Contains(%d) = %v, want %v", p, !want, want)
		}
	}
	if _, err := ParseWindow("0", "3"); err == nil {
		t.Error("window min 0 accepted")
	}
	if _, err := ParseWindow("3", "2"); err == nil {
		t.Error("inverted window accepted")
	}
}
