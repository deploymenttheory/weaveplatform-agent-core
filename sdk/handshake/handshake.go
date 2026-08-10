// Package handshake is the shared vocabulary of the core↔module handshake:
// the environment core sets, the one stdout line the module answers with,
// and the exit code that means "protocol out of window". Both sides import
// this package; neither side imports the other.
//
// The flow (PROTOCOL.md owns the normative text):
//
//	core → module    env: EnvProtocolMin, EnvProtocolMax, EnvToken,
//	                      EnvHostAddr, EnvSocketDir, EnvConfig
//	module → core    stdout: WEAVE|1|<protocol>|<network>|<addr>
//	core → module    gRPC ModuleService.Init on <addr>
//	module → core    gRPC dial EnvHostAddr presenting the token
package handshake

import (
	"fmt"
	"strconv"
	"strings"
)

// Environment variables core sets before exec'ing a module.
const (
	// EnvProtocolMin and EnvProtocolMax advertise core's accepted window.
	EnvProtocolMin = "WEAVE_PROTOCOL_MIN"
	EnvProtocolMax = "WEAVE_PROTOCOL_MAX"
	// EnvToken is the one-time token the module presents when dialing the
	// host address; core binds the connection to the module's identity.
	EnvToken = "WEAVE_HANDSHAKE_TOKEN"
	// EnvHostAddr is where core serves this module's host services.
	EnvHostAddr = "WEAVE_HOST_ADDR"
	// EnvSocketDir is the core-owned directory the module's own socket
	// must be created in (ignored for Windows named pipes).
	EnvSocketDir = "WEAVE_SOCKET_DIR"
	// EnvConfig is the path to the module's config document, mode 0600.
	EnvConfig = "WEAVE_CONFIG"
)

// LineVersion is the handshake format version — the leading field of the
// stdout line, distinct from the protocol integer that follows it.
const LineVersion = 1

// ExitProtocolUnsupported (EX_CONFIG) is the exit code a module uses when
// its protocol falls outside the advertised window. Core records
// "protocol unsupported" and does not restart — refusal is clean, never a
// crash loop.
const ExitProtocolUnsupported = 78

// TokenMetadataKey is the gRPC metadata key the module presents the
// one-time token under when dialing the host address.
const TokenMetadataKey = "weave-handshake-token"

// Line is the module's one-line stdout answer.
type Line struct {
	// Protocol the module speaks (already validated against the window).
	Protocol uint32
	// Network is "unix" or "winpipe".
	Network string
	// Addr is the socket path or pipe name the module is listening on.
	Addr string
}

// Format renders the line without a trailing newline.
func (l Line) Format() string {
	return fmt.Sprintf("WEAVE|%d|%d|%s|%s", LineVersion, l.Protocol, l.Network, l.Addr)
}

// Parse parses a module's stdout line.
func Parse(s string) (Line, error) {
	parts := strings.Split(strings.TrimSpace(s), "|")
	if len(parts) != 5 || parts[0] != "WEAVE" {
		return Line{}, fmt.Errorf("handshake: malformed line %q", s)
	}
	v, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || v != LineVersion {
		return Line{}, fmt.Errorf("handshake: unsupported line version %q", parts[1])
	}
	p, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || p == 0 {
		return Line{}, fmt.Errorf("handshake: bad protocol %q", parts[2])
	}
	if parts[3] != "unix" && parts[3] != "winpipe" {
		return Line{}, fmt.Errorf("handshake: unknown network %q", parts[3])
	}
	if parts[4] == "" {
		return Line{}, fmt.Errorf("handshake: empty address")
	}
	return Line{Protocol: uint32(p), Network: parts[3], Addr: parts[4]}, nil
}

// Window is core's advertised protocol range as read from (or written to)
// the environment.
type Window struct {
	Min, Max uint32
}

// Contains reports whether p falls inside the window.
func (w Window) Contains(p uint32) bool { return p >= w.Min && p <= w.Max }

// ParseWindow reads the two env values (as strings) into a Window.
func ParseWindow(min, max string) (Window, error) {
	lo, err := strconv.ParseUint(min, 10, 32)
	if err != nil {
		return Window{}, fmt.Errorf("handshake: bad %s %q", EnvProtocolMin, min)
	}
	hi, err := strconv.ParseUint(max, 10, 32)
	if err != nil {
		return Window{}, fmt.Errorf("handshake: bad %s %q", EnvProtocolMax, max)
	}
	if lo == 0 || hi < lo {
		return Window{}, fmt.Errorf("handshake: invalid window [%d,%d]", lo, hi)
	}
	return Window{Min: uint32(lo), Max: uint32(hi)}, nil
}
