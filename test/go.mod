module mddb-test

go 1.25

require (
	github.com/go-sql-driver/mysql v1.8.1
	github.com/lib/pq v1.10.9
	google.golang.org/grpc v1.76.0
	mddb v0.0.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	golang.org/x/net v0.42.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
	golang.org/x/text v0.27.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250804133106-a7a43d27e69b // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

replace mddb => ../services/mddbd
