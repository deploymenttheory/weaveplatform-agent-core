package sysinfo

import (
	"testing"
	"time"
)

func TestCollect(t *testing.T) {
	inv, err := Collect()
	if err != nil {
		t.Fatal(err)
	}
	if inv.Hostname == "" || inv.OS == "" || inv.Arch == "" {
		t.Errorf("inventory missing basics: %+v", inv)
	}
	if inv.CPUCores == 0 {
		t.Errorf("no CPU cores reported")
	}
	if time.Since(inv.CollectedAt) > time.Minute {
		t.Errorf("stale collection timestamp %v", inv.CollectedAt)
	}
}

func TestSetConfigAndHealth(t *testing.T) {
	m := New()
	if err := m.SetConfig([]byte(`{"interval_seconds": 30}`)); err != nil {
		t.Fatal(err)
	}
	if m.cfg.IntervalSeconds != 30 {
		t.Errorf("config not applied: %+v", m.cfg)
	}
	h := m.Health()
	if h.Reason != "no collection yet" {
		t.Errorf("pre-collection health = %+v", h)
	}
}
