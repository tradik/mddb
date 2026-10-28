// Package benchenv holds what the benchmark clients share with
// docker-compose.benchmark.yml: the password of the throwaway databases,
// read from the environment rather than written into each client.
package benchenv

import (
	"fmt"
	"os"
)

// PasswordVar names the variable that holds the benchmark databases'
// password. compare-all-databases.sh and the compose file default it.
const PasswordVar = "MDDB_BENCH_PASSWORD" // #nosec G101 -- the variable's name, not a password

// Password returns the benchmark password, or an error naming PasswordVar
// when it is not set.
func Password() (string, error) {
	p := os.Getenv(PasswordVar)
	if p == "" {
		return "", fmt.Errorf("%s is not set; export the password docker-compose.benchmark.yml uses", PasswordVar)
	}
	return p, nil
}

// MustPassword is Password for a main: it exits with the error instead.
func MustPassword() string {
	p, err := Password()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exit(1)
	}
	return p
}

var exit = os.Exit
