package capability

import "golang.org/x/sys/windows/registry"

func probeOS(set Set) {
	// hypervisor.channel: a Hyper-V guest exposes the virtualization
	// guest parameters key.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters`, registry.QUERY_VALUE)
	if err == nil {
		k.Close()
		set["hypervisor.channel"] = map[string]string{"kind": "hvsocket"}
	}
}
