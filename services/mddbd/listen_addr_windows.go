//go:build windows

package main

import "fmt"

// restrictSocketToOwner refuses on Windows, which is why UDS listeners are not
// available there (WIN-002).
//
// Windows 10 does support AF_UNIX, and net.Listen("unix", …) succeeds — so this
// is not a build problem, which is exactly what makes it worth refusing.
// os.Chmod on Windows toggles the read-only attribute and nothing else: it
// cannot express "owner only". A socket created here would listen happily while
// the access restriction openListener documents quietly did not apply, and an
// IPC channel that is unprotected without saying so is worse than one that
// refuses to start.
func restrictSocketToOwner(path string) error {
	return fmt.Errorf("unix socket %q: MDDB does not serve on a unix socket on Windows, "+
		"because file permissions there cannot restrict it to its owner — "+
		"listen on tcp://127.0.0.1:<port> instead", path)
}
