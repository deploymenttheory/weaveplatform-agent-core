package capability

import "os"

func probeOS(set Set) {
	for _, dev := range []string{
		"/dev/virtio-ports/org.weave.agent.0",
		"/dev/vsock",
	} {
		if _, err := os.Stat(dev); err == nil {
			set["hypervisor.channel"] = map[string]string{"device": dev}
			break
		}
	}
}
