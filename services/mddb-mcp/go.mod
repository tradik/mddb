module github.com/tradik/mddb/services/mddb-mcp

go 1.25

require (
	github.com/kelseyhightower/envconfig v1.4.0
	google.golang.org/grpc v1.76.0
	gopkg.in/yaml.v3 v3.0.1
	mddb v0.0.0
)

require (
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250804133106-a7a43d27e69b // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

replace mddb => ../../services/mddbd
