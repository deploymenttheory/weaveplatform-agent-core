package sysinfo

import (
	"encoding/binary"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func collectOS(inv *Inventory) {
	inv.CPUCores = runtime.NumCPU()
	if v, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil {
		inv.CPUModel = v
	}
	if v, err := unix.Sysctl("hw.model"); err == nil {
		inv.HardwareModel = v
	}
	if b, err := unix.SysctlRaw("hw.memsize"); err == nil && len(b) >= 8 {
		inv.MemoryBytes = binary.LittleEndian.Uint64(b)
	}
	if tv, err := unix.SysctlTimeval("kern.boottime"); err == nil {
		boot := time.Unix(tv.Sec, int64(tv.Usec)*1000)
		inv.UptimeSeconds = uint64(time.Since(boot).Seconds())
	}
	// Serial number: ioreg without cgo or entitlements.
	if out, err := exec.Command("/usr/sbin/ioreg", "-c", "IOPlatformExpertDevice", "-d", "2", "-a").Output(); err == nil {
		if s := extractPlistKey(string(out), "IOPlatformSerialNumber"); s != "" {
			inv.SerialNumber = s
		}
	}
}

// extractPlistKey pulls <key>K</key><string>V</string> out of plist XML
// without an XML dependency; fine for one well-known key.
func extractPlistKey(plist, key string) string {
	marker := "<key>" + key + "</key>"
	i := strings.Index(plist, marker)
	if i < 0 {
		return ""
	}
	rest := plist[i+len(marker):]
	start := strings.Index(rest, "<string>")
	end := strings.Index(rest, "</string>")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(rest[start+len("<string>") : end])
}
