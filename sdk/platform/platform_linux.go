package platform

import (
	"bufio"
	"os"
	"strings"
)

func fill(info *Info) {
	if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "VERSION_ID="); ok {
			info.Version = strings.Trim(v, `"`)
		}
		if v, ok := strings.CutPrefix(line, "BUILD_ID="); ok {
			info.Build = strings.Trim(v, `"`)
		}
	}
}

// Paths returns the platform filesystem contract on Linux.
func Paths() PathSet {
	return PathSet{
		StateDir:   "/var/lib/weave",
		LogDir:     "/var/log/weave",
		RunDir:     "/run/weave",
		StagingDir: "/var/lib/weave/staging",
	}
}
