// Protocol-1 fixture: pinned to the tags that carried protocol 1 so this
// module builds exactly what a protocol-1 module was, forever. Never
// delete this directory; when protocol 2 ships, add v2/ beside it.
module github.com/deploymenttheory/weave-protocompat-v1

go 1.26.5

require github.com/deploymenttheory/weaveplatform-sdk v0.2.1

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/deploymenttheory/weaveplatform-api v0.2.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
