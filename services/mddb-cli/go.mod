module mddb-cli

go 1.27

toolchain go1.27.3

require (
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/tradik/mddb/clients/go/mddb/v2 v2.18.4
)

require github.com/inconshreveable/mousetrap v1.1.0 // indirect

// The shared Go client lives in the monorepo; pin it via a relative replace so
// the module builds standalone under GOWORK=off (matching CI).
replace github.com/tradik/mddb/clients/go/mddb/v2 => ../../clients/go/mddb
