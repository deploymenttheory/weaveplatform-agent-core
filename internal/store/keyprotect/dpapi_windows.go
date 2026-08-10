package keyprotect

import (
	"fmt"
	"unsafe"

	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/foundation"
	crypt "github.com/deploymenttheory/go-bindings-win32/bindings/win32/security/cryptography"
	"golang.org/x/sys/windows/registry"
)

// New returns the platform protector: DPAPI machine scope with secondary
// entropy derived from the machine GUID. Machine scope alone lets ANY
// local process CryptUnprotectData the blob; binding secondary entropy to
// the machine GUID (which is not stored beside store.key) means a stolen
// store.key cannot be unsealed on a different machine, and raises the bar
// for local unseal to also knowing the machine GUID.
func New() Protector { return dpapiProtector{} }

type dpapiProtector struct{}

const dpapiFlags = crypt.CRYPTPROTECT_UI_FORBIDDEN | crypt.CRYPTPROTECT_LOCAL_MACHINE

// machineEntropy reads HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid.
// It is per-install, machine-bound, and not written next to the key.
func machineEntropy() ([]byte, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return nil, fmt.Errorf("keyprotect: opening Cryptography key: %w", err)
	}
	defer k.Close()
	guid, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return nil, fmt.Errorf("keyprotect: reading MachineGuid: %w", err)
	}
	return []byte("weave-store-v1:" + guid), nil
}

func sealOp(in, entB, out *crypt.CRYPT_INTEGER_BLOB) error {
	return crypt.CryptProtectData(in, "weave store master key", entB, nil, dpapiFlags, out)
}

func unsealOp(in, entB, out *crypt.CRYPT_INTEGER_BLOB) error {
	return crypt.CryptUnprotectData(in, nil, entB, nil, dpapiFlags, out)
}

func (dpapiProtector) Seal(key []byte) ([]byte, error) {
	ent, err := machineEntropy()
	if err != nil {
		return nil, err
	}
	return dpapi(key, ent, sealOp)
}

func (dpapiProtector) Unseal(sealed []byte) ([]byte, error) {
	ent, err := machineEntropy()
	if err != nil {
		return nil, err
	}
	return dpapi(sealed, ent, unsealOp)
}

func dpapi(data, entropy []byte, op func(in, ent, out *crypt.CRYPT_INTEGER_BLOB) error) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("keyprotect: empty input")
	}
	in := crypt.CRYPT_INTEGER_BLOB{CbData: uint32(len(data)), PbData: &data[0]}
	ent := crypt.CRYPT_INTEGER_BLOB{CbData: uint32(len(entropy)), PbData: &entropy[0]}
	var out crypt.CRYPT_INTEGER_BLOB
	if err := op(&in, &ent, &out); err != nil {
		return nil, fmt.Errorf("keyprotect: dpapi: %w", err)
	}
	defer foundation.LocalFree(foundation.HLOCAL(unsafe.Pointer(out.PbData))) //nolint:errcheck
	result := make([]byte, out.CbData)
	copy(result, unsafe.Slice(out.PbData, out.CbData))
	return result, nil
}
