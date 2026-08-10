package capability

import "os"

func probeOS(set Set) {
	// hypervisor.channel: present when running as a VM guest with a Weave
	// virtio-serial channel. The device node is the probe.
	for _, dev := range []string{
		"/dev/cu.org.weave.agent.0",
		"/dev/virtio-ports/org.weave.agent.0",
	} {
		if _, err := os.Stat(dev); err == nil {
			set["hypervisor.channel"] = map[string]string{"device": dev}
			break
		}
	}
}
