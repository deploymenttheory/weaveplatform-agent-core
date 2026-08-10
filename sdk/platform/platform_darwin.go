package platform

import (
	"os"
	"os/exec"
	"strings"
)

func fill(info *Info) {
	if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}
	// sw_vers is stable public interface; parsing a plist adds nothing.
	if out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output(); err == nil {
		info.Version = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("/usr/bin/sw_vers", "-buildVersion").Output(); err == nil {
		info.Build = strings.TrimSpace(string(out))
	}
}

// Paths returns the platform filesystem contract on macOS.
func Paths() PathSet {
	return PathSet{
		StateDir:   "/Library/Application Support/Weave",
		LogDir:     "/Library/Logs/Weave",
		RunDir:     "/var/run/weave",
		StagingDir: "/Library/Application Support/Weave/staging",
	}
}
