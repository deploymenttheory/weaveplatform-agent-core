// Package modulesdk is the module runtime. A module author implements the
// Module interface and calls Serve:
//
//	func main() { modulesdk.Serve(sysinfo.New()) }
//
// Serve reads the handshake environment, negotiates the protocol, listens
// on the module's socket, prints the one-line handshake answer, and
// dispatches core's lifecycle RPCs onto the Module interface. The Host
// handed to Init is a client of core's per-module host services.
//
// Implemented in milestone M1.
package modulesdk
