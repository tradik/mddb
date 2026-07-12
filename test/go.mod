module mddb-test

go 1.26

toolchain go1.26.4

require (
	github.com/go-sql-driver/mysql v1.10.0
	github.com/lib/pq v1.12.3
	go.mongodb.org/mongo-driver v1.17.9
	google.golang.org/grpc v1.82.0
	mddb v0.0.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/klauspost/compress v1.19.0 // indirect
	github.com/montanaflynn/stats v0.9.0 // indirect
	github.com/xdg-go/pbkdf2 v1.0.0 // indirect
	github.com/xdg-go/scram v1.2.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace mddb => ../services/mddbd
