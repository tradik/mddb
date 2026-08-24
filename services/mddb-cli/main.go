// Command mddb-cli is the command-line client for MDDB.
//
// TEST-001: every command used to be an anonymous RunE closure inside a
// 1447-line main(), where no test could reach one. main() now builds the
// command tree through newRootCmd(), which a test can execute against an
// httptest server the same way a shell executes it against a real one.
package main

import (
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
