module github.com/deploymenttheory/weaveplatform-agent

go 1.26.5

require (
	github.com/deploymenttheory/go-bindings-win32 v0.3.1
	go.etcd.io/bbolt v1.5.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	google.golang.org/grpc v1.83.1
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/deploymenttheory/weaveplatform-agent/sdk v0.0.0
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/deploymenttheory/weaveplatform-agent/sdk => ./sdk
