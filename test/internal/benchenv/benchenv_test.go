package benchenv

import (
	"os"
	"testing"
)

func TestPasswordFromTheEnvironment(t *testing.T) {
	t.Setenv(PasswordVar, "s3cret")
	if p, err := Password(); err != nil || p != "s3cret" {
		t.Fatalf("Password() = %q, %v", p, err)
	}
	if p := MustPassword(); p != "s3cret" {
		t.Fatalf("MustPassword() = %q", p)
	}
}

func TestAMissingPasswordNamesTheVariable(t *testing.T) {
	t.Setenv(PasswordVar, "")
	if _, err := Password(); err == nil {
		t.Fatal("Password() with the variable unset returned no error")
	}
	code := -1
	exit = func(c int) { code = c }
	defer func() { exit = os.Exit }()
	MustPassword()
	if code != 1 {
		t.Fatalf("MustPassword exited with %d, want 1", code)
	}
}
