package main

import (
	"errors"
	"testing"
)

// With auth disabled there is no secret to sign with, so asking for a token
// must fail loudly instead of returning an unsigned or empty one.
func TestCovFGenerateJWTRefusesWhenAuthIsDisabled(t *testing.T) {
	a, _ := covFAdapter(t)
	token, exp, err := a.GenerateJWT("admin", true)
	if !errors.Is(err, ErrAuthNotEnabled) || token != "" || exp != 0 {
		t.Fatalf("GenerateJWT = %q, %d, %v; want ErrAuthNotEnabled", token, exp, err)
	}
}

// With auth enabled the token is signed for the user and validates against
// the configured secret.
func TestCovFGenerateJWTIssuesAValidToken(t *testing.T) {
	a, s := covFAdapter(t)
	withAuth(t, s)
	token, exp, err := a.GenerateJWT("admin", true)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ValidateJWT(token, "test-secret-do-not-ship")
	if err != nil {
		t.Fatalf("issued token does not validate: %v", err)
	}
	if claims.Username != "admin" || !claims.Admin || exp <= 0 {
		t.Errorf("claims = %+v, exp = %d", claims, exp)
	}
}
