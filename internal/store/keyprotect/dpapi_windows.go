package keyprotect

import (
	"fmt"
	"unsafe"

	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/foundation"
	crypt "github.com/deploymenttheory/go-bindings-win32/bindings/win32/security/cryptography"
)

// New returns the platform protector: DPAPI machine scope, so the sealed
// key survives reboots and profile changes but never leaves this machine.
func New() Protector { return dpapiProtector{} }

type dpapiProtector struct{}

const dpapiFlags = crypt.CRYPTPROTECT_UI_FORBIDDEN | crypt.CRYPTPROTECT_LOCAL_MACHINE

func (dpapiProtector) Seal(key []byte) ([]byte, error) {
	return dpapi(key, func(in, out *crypt.CRYPT_INTEGER_BLOB) error {
		return crypt.CryptProtectData(in, "weave store master key", nil, nil, dpapiFlags, out)
	})
}

func (dpapiProtector) Unseal(sealed []byte) ([]byte, error) {
	return dpapi(sealed, func(in, out *crypt.CRYPT_INTEGER_BLOB) error {
		return crypt.CryptUnprotectData(in, nil, nil, nil, dpapiFlags, out)
	})
}

func dpapi(data []byte, op func(in, out *crypt.CRYPT_INTEGER_BLOB) error) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("keyprotect: empty input")
	}
	in := crypt.CRYPT_INTEGER_BLOB{CbData: uint32(len(data)), PbData: &data[0]}
	var out crypt.CRYPT_INTEGER_BLOB
	if err := op(&in, &out); err != nil {
		return nil, fmt.Errorf("keyprotect: dpapi: %w", err)
	}
	defer foundation.LocalFree(foundation.HLOCAL(unsafe.Pointer(out.PbData))) //nolint:errcheck
	result := make([]byte, out.CbData)
	copy(result, unsafe.Slice(out.PbData, out.CbData))
	return result, nil
}
