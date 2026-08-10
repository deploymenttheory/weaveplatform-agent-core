// Command sysinfo is the platform's own module: host inventory and
// heartbeat. It is deliberately small — it exists to prove the harness and
// to be copied as the module template.
package main

import (
	"github.com/deploymenttheory/weaveplatform-agent-modules/sysinfo/internal/sysinfo"
	"github.com/deploymenttheory/weaveplatform-sdk/modulesdk"
)

func main() { modulesdk.Serve(sysinfo.New()) }
