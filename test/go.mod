module mddb-test

go 1.27

toolchain go1.27.0

require (
	github.com/go-sql-driver/mysql v1.10.0
	github.com/lib/pq v1.12.3
	go.mongodb.org/mongo-driver v1.17.9
	google.golang.org/grpc v1.83.2
	mddb v0.0.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/montanaflynn/stats v0.12.4 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.2.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace mddb => ../services/mddbd
