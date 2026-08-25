package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func collectOS(inv *Inventory) {
	inv.CPUCores = runtime.NumCPU()

	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err == nil {
		inv.MemoryBytes = uint64(si.Totalram) * uint64(si.Unit)
		inv.UptimeSeconds = uint64(si.Uptime)
	}

	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if name, ok := strings.CutPrefix(sc.Text(), "model name"); ok {
				inv.CPUModel = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), ":"))
				break
			}
		}
		f.Close()
	}

	readTrimmed := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	inv.HardwareModel = readTrimmed("/sys/class/dmi/id/product_name")
	// Often root-only; empty is fine.
	inv.SerialNumber = readTrimmed("/sys/class/dmi/id/product_serial")
}
