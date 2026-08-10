//go:build !dev

package verify

import (
	"fmt"
	"log/slog"
	"runtime"
	"unsafe"

	win32 "github.com/deploymenttheory/go-bindings-win32/bindings/runtime/win32"
	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/foundation"
	crypt "github.com/deploymenttheory/go-bindings-win32/bindings/win32/security/cryptography"
	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/security/wintrust"
	"github.com/deploymenttheory/weaveplatform-agent/internal/supervise"
	"github.com/deploymenttheory/weaveplatform-api/manifest"
)

// newVerifier (release, Windows).
func newVerifier(_ *slog.Logger) supervise.Verifier {
	return supervise.VerifierFunc(authenticodeVerify)
}

// authenticodeVerify authenticates a module binary on Windows: a full
// WinVerifyTrust chain validation (SmartScreen protects nobody here —
// modules are fetched after install), then the signing certificate's
// subject pinned against the manifest.
func authenticodeVerify(path string, m *manifest.Manifest) error {
	if m.Signing == nil || m.Signing.AuthenticodeSubject == "" {
		return fmt.Errorf("verify: manifest for %s pins no authenticode_subject; refusing", m.ID)
	}

	fileInfo := wintrust.WINTRUST_FILE_INFO{
		PcwszFilePath: foundation.PWSTR(win32.UTF16Ptr(path)),
	}
	fileInfo.CbStruct = uint32(unsafe.Sizeof(fileInfo))

	wtd := wintrust.WINTRUST_DATA{
		DwUIChoice:          wintrust.WTD_UI_NONE,
		FdwRevocationChecks: wintrust.WTD_REVOKE_NONE,
		DwUnionChoice:       wintrust.WTD_CHOICE_FILE,
		DwStateAction:       wintrust.WTD_STATEACTION_VERIFY,
	}
	wtd.CbStruct = uint32(unsafe.Sizeof(wtd))
	// fileInfo is stored into a union modeled as a byte array, which the GC
	// does not scan as a pointer slot — so nothing keeps fileInfo (and its
	// UTF-16 path buffer) alive across the two WinVerifyTrustEx calls
	// without an explicit KeepAlive. The CLOSE call is NOT deferred, so the
	// KeepAlive at the end of the function covers the whole span.
	*(*unsafe.Pointer)(unsafe.Pointer(&wtd.Anonymous.Data[0])) = unsafe.Pointer(&fileInfo)

	status := wintrust.WinVerifyTrustEx(0, &wintrust.WINTRUST_ACTION_GENERIC_VERIFY_V2, &wtd)

	verifyErr := error(nil)
	if status != 0 {
		verifyErr = fmt.Errorf("verify: WinVerifyTrust rejected %s: 0x%08x", path, uint32(status))
	} else {
		subject, serr := signerSubject(wtd.HWVTStateData)
		switch {
		case serr != nil:
			verifyErr = fmt.Errorf("verify: %s: %w", path, serr)
		case subject != m.Signing.AuthenticodeSubject:
			verifyErr = fmt.Errorf("verify: %s signed by %q, manifest pins %q", path, subject, m.Signing.AuthenticodeSubject)
		}
	}

	// Release the trust state (whether verify passed or failed).
	wtd.DwStateAction = wintrust.WTD_STATEACTION_CLOSE
	wintrust.WinVerifyTrustEx(0, &wintrust.WINTRUST_ACTION_GENERIC_VERIFY_V2, &wtd)
	runtime.KeepAlive(&fileInfo)
	return verifyErr
}

// signerSubject walks the trust provider's chain to the leaf signing
// certificate and returns its simple display name.
func signerSubject(state foundation.HANDLE) (string, error) {
	provData := wintrust.WTHelperProvDataFromStateData(state)
	if provData == nil {
		return "", fmt.Errorf("no provider data from trust state")
	}
	sgnr := wintrust.WTHelperGetProvSignerFromChain(provData, 0, false, 0)
	if sgnr == nil {
		return "", fmt.Errorf("no signer in trust chain")
	}
	cert := wintrust.WTHelperGetProvCertFromChain(sgnr, 0)
	if cert == nil || cert.PCert == nil {
		return "", fmt.Errorf("no leaf certificate in trust chain")
	}
	buf := make([]uint16, 512)
	n := crypt.CertGetNameString(cert.PCert, crypt.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil,
		foundation.PWSTR(&buf[0]), uint32(len(buf)))
	if n <= 1 {
		return "", fmt.Errorf("certificate has no subject name")
	}
	return win32.UTF16ToString(&buf[0]), nil
}
