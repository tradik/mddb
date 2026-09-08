module mddb

go 1.27

toolchain go1.27.0

require (
	github.com/99designs/gqlgen v0.17.95
	github.com/agnivade/levenshtein v1.2.1
	github.com/bits-and-blooms/bloom/v3 v3.7.1
	github.com/blevesearch/snowballstem v0.9.0
	github.com/coder/hnsw v0.6.1
	github.com/goccy/go-json v0.10.6
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang/snappy v1.0.0
	github.com/klauspost/compress v1.20.0
	github.com/minio/minio-go/v7 v7.3.0
	github.com/quic-go/quic-go v0.62.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/tidwall/rtree v1.11.1
	github.com/vektah/gqlparser/v2 v2.5.37
	go.etcd.io/bbolt v1.5.0
	golang.org/x/crypto v0.56.0
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/bits-and-blooms/bitset v1.25.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chewxy/math32 v1.11.2 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	github.com/tidwall/geoindex v1.7.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/viterin/partial v1.1.0 // indirect
	github.com/viterin/vek v0.4.3 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
)

// WIN-003. github.com/coder/hnsw does not compile for Windows: its
// SavedGraph.Save uses github.com/google/renameio, which has no Windows build
// (neither v1 nor v2 defines TempFile there). Go compiles a package as a whole,
// so this blocks the whole hnsw package for us even though we never call Save —
// we persist vectors ourselves in internal/vector/vector_store.go.
//
// The fork is a single commit against upstream main: it replaces that one call
// with os.CreateTemp + fsync + os.Rename. Sent upstream as
// https://github.com/coder/hnsw/pull/24.
//
// REMOVE THIS once that PR is merged and tagged — tracked as WIN-005.
replace github.com/coder/hnsw => github.com/tradik/hnsw v0.6.2-0.20260824091751-ad04fee59f41
