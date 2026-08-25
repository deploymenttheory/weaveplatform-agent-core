// Command weavectl is the operator CLI over core's control socket.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/deploymenttheory/weaveplatform-agent/internal/controlsock"
	"github.com/deploymenttheory/weaveplatform-agent/internal/layout"
	controlv1 "github.com/deploymenttheory/weaveplatform-agent/sdk/gen/go/weave/control/v1"
)

const usage = `weavectl — Weave platform agent control

Usage:
  weavectl [-socket PATH] status                        core status
  weavectl [-socket PATH] modules                       supervised module states
  weavectl [-socket PATH] surfaces                      declared UI surfaces
  weavectl [-socket PATH] install <module> [version]    install from the channel manifest
  weavectl [-socket PATH] install -local <dir>          install from a local directory (dev)
  weavectl [-socket PATH] rollback <module>             flip to the retained previous version
`

func main() {
	socket := flag.String("socket", "", "control socket path (default: the platform layout's)")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	cmd := flag.Arg(0)
	if cmd == "" {
		flag.Usage()
		os.Exit(2)
	}

	addr := *socket
	if addr == "" {
		addr = layout.Resolve("").ControlSocket()
	}

	client, conn, err := controlsock.Dial(addr)
	if err != nil {
		fatal("connecting to %s: %v", addr, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch cmd {
	case "status":
		st, err := client.Status(ctx, &controlv1.StatusRequest{})
		if err != nil {
			fatal("status: %v", err)
		}
		fmt.Printf("core      %s\n", st.GetCoreVersion())
		fmt.Printf("protocol  [%d,%d]\n", st.GetProtocol().GetMin(), st.GetProtocol().GetMax())
		fmt.Printf("device    %s\n", st.GetDeviceId())
		fmt.Printf("enrolled  %v\n", st.GetEnrolled())
		fmt.Printf("uptime    %s\n", (time.Duration(st.GetUptimeSeconds()) * time.Second).String())

	case "modules":
		resp, err := client.Modules(ctx, &controlv1.ModulesRequest{})
		if err != nil {
			fatal("modules: %v", err)
		}
		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "MODULE\tVERSION\tPROTO\tSTATE\tPID\tRESTARTS\tHEALTH")
		for _, m := range resp.GetModules() {
			health := "-"
			if h := m.GetHealth(); h != nil {
				health = h.GetStatus().String()
				if r := h.GetReason(); r != "" {
					health += " (" + r + ")"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%d\t%d\t%s\n",
				m.GetId(), m.GetVersion(), m.GetProtocol(), m.GetState(),
				m.GetPid(), m.GetRestarts(), health)
		}
		w.Flush()

	case "surfaces":
		resp, err := client.Surfaces(ctx, &controlv1.SurfacesRequest{})
		if err != nil {
			fatal("surfaces: %v", err)
		}
		if len(resp.GetModules()) == 0 {
			fmt.Println("no surfaces declared")
			return
		}
		for _, ms := range resp.GetModules() {
			for _, s := range ms.GetSurfaces() {
				fmt.Printf("%s/%s  kind=%s  title=%q  (%d bytes)\n",
					ms.GetModuleId(), s.GetId(), s.GetKind(), s.GetTitle(), len(s.GetData()))
			}
		}

	case "install":
		req := &controlv1.InstallRequest{}
		args := flag.Args()[1:]
		if len(args) >= 2 && args[0] == "-local" {
			abs, err := filepath.Abs(args[1])
			if err != nil {
				fatal("resolving path: %v", err)
			}
			req.LocalPath = abs
		} else if len(args) >= 1 {
			req.ModuleId = args[0]
			if len(args) >= 2 {
				req.Version = args[1]
			}
		} else {
			flag.Usage()
			os.Exit(2)
		}
		// Install includes the health gate; give it time.
		ictx, icancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer icancel()
		resp, err := client.Install(ictx, req)
		if err != nil {
			fatal("install: %v", err)
		}
		fmt.Printf("installed %s\n", resp.GetInstalledVersion())

	case "rollback":
		if flag.Arg(1) == "" {
			flag.Usage()
			os.Exit(2)
		}
		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer rcancel()
		resp, err := client.Rollback(rctx, &controlv1.RollbackRequest{ModuleId: flag.Arg(1)})
		if err != nil {
			fatal("rollback: %v", err)
		}
		fmt.Printf("rolled back to %s\n", resp.GetRolledBackTo())

	default:
		flag.Usage()
		os.Exit(2)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "weavectl: "+format+"\n", args...)
	os.Exit(1)
}
