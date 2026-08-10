package sysinfo

import (
	"runtime"
	"unsafe"

	"github.com/deploymenttheory/go-bindings-wmi/bindings/cim/cimv2"
	"github.com/deploymenttheory/go-bindings-wmi/runtime/wmi"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func collectOS(inv *Inventory) {
	inv.CPUCores = runtime.NumCPU()
	inv.UptimeSeconds = uint64(windows.DurationSinceBoot().Seconds())

	var statex memoryStatusEx
	statex.Length = uint32(unsafe.Sizeof(statex))
	if err := globalMemoryStatusEx(&statex); err == nil {
		inv.MemoryBytes = statex.TotalPhys
	}

	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("ProcessorNameString"); err == nil {
			inv.CPUModel = v
		}
		k.Close()
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("SystemProductName"); err == nil {
			inv.HardwareModel = v
		}
		k.Close()
	}
	collectWMI(inv)
}

// collectWMI fills the fields only WMI serves. Connect/use/close stays on
// one goroutine: the WMI Service is COM-thread-pinned.
func collectWMI(inv *Inventory) {
	svc, err := wmi.Connect(`root\cimv2`)
	if err != nil {
		return
	}
	defer svc.Close()
	if bios, err := cimv2.QueryOneWin32BIOS(svc, ""); err == nil && bios != nil {
		inv.SerialNumber = bios.SerialNumber
	}
	if cs, err := cimv2.QueryOneWin32ComputerSystem(svc, ""); err == nil && cs != nil && cs.Model != "" {
		inv.HardwareModel = cs.Model
	}
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
)

func globalMemoryStatusEx(m *memoryStatusEx) error {
	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(m)))
	if r1 == 0 {
		return err
	}
	return nil
}
