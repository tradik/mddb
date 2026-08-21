module mddb-cli

go 1.27

toolchain go1.27.0

require (
	github.com/spf13/cobra v1.10.2
	mddb-client v0.0.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
)

// The shared Go client lives in the monorepo; pin it via a relative replace so
// the module builds standalone under GOWORK=off (matching CI).
replace mddb-client => ../../clients/go/mddb
