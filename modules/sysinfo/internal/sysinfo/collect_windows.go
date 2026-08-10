package sysinfo

import (
	"runtime"
	"unsafe"

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
	// Serial number needs WMI (Win32_BIOS.SerialNumber); lands with the
	// go-bindings-wmi integration.
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
