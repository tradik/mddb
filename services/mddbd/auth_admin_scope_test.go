package main

import (
	"context"
	"errors"
	"testing"
)

// SEC-020: admin on one collection is admin of that collection only. IsAdmin
// feeds the global Admin flag of login and API-key tokens, and CheckPermission
// lets that flag through everywhere, so it must mean admin on "*".
func TestCollectionAdminIsNotGlobalAdmin(t *testing.T) {
	am, _, cleanup := setupTestAuthManager(t)
	defer cleanup()
	if _, err := am.CreateUser("carol", "password123"); err != nil {
		t.Fatal(err)
	}
	if err := am.SetPermission(&Permission{Username: "carol", Collection: "docs", Read: true, Write: true, Admin: true}); err != nil {
		t.Fatal(err)
	}
	if am.IsAdmin("carol") {
		t.Fatal("admin on collection docs made carol a global admin")
	}

	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "carol", Admin: am.IsAdmin("carol")})
	if err := am.CheckPermission(ctx, "docs", PermAdmin); err != nil {
		t.Errorf("admin on her own collection: %v", err)
	}
	if err := am.CheckPermission(ctx, "payroll", PermRead); !errors.Is(err, ErrForbidden) {
		t.Errorf("read on another collection: %v, want ErrForbidden", err)
	}
	if err := am.CheckPermission(ctx, "*", PermAdmin); !errors.Is(err, ErrForbidden) {
		t.Errorf("global admin operation: %v, want ErrForbidden", err)
	}

	if err := am.SetPermission(&Permission{Username: "carol", Collection: "*", Admin: true}); err != nil {
		t.Fatal(err)
	}
	if !am.IsAdmin("carol") {
		t.Error("admin on * should make carol a global admin")
	}
}
