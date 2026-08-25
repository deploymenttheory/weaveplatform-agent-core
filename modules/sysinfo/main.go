// Command sysinfo is the platform's own module: host inventory and
// heartbeat. It is deliberately small — it exists to prove the harness and
// to be copied as the module template. It lives in the platform repository
// as its own Go module so that it pins the sdk by version exactly as a
// product module does; nothing here may import core.
package main

import (
	"github.com/deploymenttheory/weaveplatform-agent-core/modules/sysinfo/internal/sysinfo"
	"github.com/deploymenttheory/weaveplatform-agent-core/sdk/modulesdk"
)

func main() { modulesdk.Serve(sysinfo.New()) }
