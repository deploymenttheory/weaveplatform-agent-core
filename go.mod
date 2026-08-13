module github.com/deploymenttheory/weaveplatform-agent

go 1.26.5

require (
	github.com/deploymenttheory/go-bindings-win32 v0.2.1
	github.com/deploymenttheory/weaveplatform-api v0.8.0
	github.com/deploymenttheory/weaveplatform-sdk v0.6.0
	go.etcd.io/bbolt v1.5.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.43.0
	google.golang.org/grpc v1.83.0
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// TEMPORARY: the hypervisor channel's authentication handshake lives in
// weaveplatform-api's hvchannel package and is not in the published v0.8.0. Until
// that repo is tagged, a sibling checkout is the only way to build against it.
// Delete this line and pin the release once it exists.
replace github.com/deploymenttheory/weaveplatform-api => ../weaveplatform-api
