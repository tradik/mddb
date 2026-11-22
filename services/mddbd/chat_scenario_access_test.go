package main

import (
	"net/http"
	"testing"

	json "mddb/internal/jsonx"
)

// #441: GET /v1/chat/scenarios/{name}/access, on chatServer's users
// (alice: docs; tina: tenant t1, "*"; root: admin; nobby: tenant t2, "*")
// plus group and wildcard grants.

// accessFixture adds bob (group "readers" on docs), carl (group "all" on "*",
// and "zeta" on docs to check the group order), dora (her own read on "*",
// disabled) and erin (nothing), and the scenario "phil" on kb (an alias of
// docs) and private.
func accessFixture(t *testing.T) *Server {
	t.Helper()
	s := chatServer(t)
	am := s.AuthManager
	for _, u := range []string{"bob", "carl", "dora", "erin"} {
		if _, err := am.CreateUser(u, "password-123"); err != nil {
			t.Fatal(err)
		}
	}
	am.users["dora"].Disabled = true
	for name, members := range map[string][]string{"readers": {"bob"}, "all": {"carl"}, "zeta": {"carl"}} {
		if _, err := am.CreateGroup(name, "", members); err != nil {
			t.Fatal(err)
		}
	}
	for _, gp := range []*GroupPermission{
		{GroupName: "readers", Collection: "docs", Read: true},
		{GroupName: "all", Collection: "*", Read: true},
		{GroupName: "zeta", Collection: "docs", Read: true},
	} {
		if err := am.SetGroupPermission(gp); err != nil {
			t.Fatal(err)
		}
	}
	if err := am.SetPermission(&Permission{Username: "dora", Collection: "*", Read: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("kb", "docs"); err != nil {
		t.Fatal(err)
	}
	for name, colls := range map[string]string{"phil": `["kb","private"]`, "open": `[]`} {
		if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/"+name, `{"allowedCollections":`+colls+`}`); rec.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	return s
}

func getAccess(t *testing.T, s *Server, c *JWTClaims, name string) (int, chatScenarioAccess) {
	t.Helper()
	rec := chatREST(t, s, c, http.MethodGet, "/v1/chat/scenarios/"+name+"/access", "")
	var out chatScenarioAccess
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, out
}

// summary is "full|via,via" per user, for compact comparisons.
func summary(out chatScenarioAccess) map[string]string {
	m := map[string]string{}
	for _, u := range out.Users {
		v := "part|"
		if u.Full {
			v = "full|"
		}
		for i, g := range u.Grants {
			if i > 0 {
				v += ","
			}
			v += g.Collection + "=" + g.Via
		}
		m[u.Username] = v
	}
	return m
}

func TestChatScenarioAccessListsEveryUserWithTheRule(t *testing.T) {
	s := accessFixture(t)
	code, out := getAccess(t, s, root, "phil")
	if code != http.StatusOK || out.Scenario != "phil" || !out.AuthEnabled ||
		len(out.Collections) != 2 || out.Collections[0] != "docs" || out.Collections[1] != "private" {
		t.Fatalf("%d %+v", code, out)
	}
	want := map[string]string{
		"alice": "part|docs=user",
		"bob":   "part|docs=group:readers",
		"carl":  "full|docs=group:all:*,private=group:all:*",
		"dora":  "full|docs=user:*,private=user:*",
		"erin":  "part|",
		"nobby": "part|", // tenant t2: its "*" grant stops at the namespace
		"root":  "full|docs=admin,private=admin",
		"tina":  "part|",
	}
	got := summary(out)
	for u, w := range want {
		if got[u] != w {
			t.Errorf("%s: %q, want %q", u, got[u], w)
		}
	}
	names := []string{}
	for _, u := range out.Users {
		names = append(names, u.Username)
		if u.Admin != (u.Username == "root") || u.Disabled != (u.Username == "dora") {
			t.Errorf("%s: admin %v disabled %v", u.Username, u.Admin, u.Disabled)
		}
	}
	if len(names) != len(want) || names[0] != "alice" || names[len(names)-1] != "tina" {
		t.Errorf("order: %v", names)
	}
}

func TestChatScenarioAccessTenantAndEmptyScenario(t *testing.T) {
	s := accessFixture(t)
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/t", `{"allowedCollections":["t1/kb"]}`); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	got := summary(mustAccess(t, s, "t"))
	if got["tina"] != "full|t1/kb=user:*" || got["nobby"] != "part|" || got["root"] != "full|t1/kb=admin" {
		t.Errorf("tenant collection: %v", got)
	}
	// No collections: everyone sees it, as chatScenarioReadable does.
	for u, v := range summary(mustAccess(t, s, "open")) {
		if v != "full|" {
			t.Errorf("open, %s: %q", u, v)
		}
	}
}

func mustAccess(t *testing.T, s *Server, name string) chatScenarioAccess {
	t.Helper()
	code, out := getAccess(t, s, root, name)
	if code != http.StatusOK {
		t.Fatalf("%s: %d", name, code)
	}
	return out
}

func TestChatScenarioAccessIsForAnAdministrator(t *testing.T) {
	s := accessFixture(t)
	for _, c := range []*JWTClaims{alice, tina} {
		for _, name := range []string{"phil", "ghost"} {
			if code, _ := getAccess(t, s, c, name); code != http.StatusForbidden {
				t.Errorf("%s on %s: %d, want 403", c.Username, name, code)
			}
		}
	}
	if code, _ := getAccess(t, s, root, "ghost"); code != http.StatusNotFound {
		t.Errorf("unknown scenario: %d", code)
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/phil/access", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: %d", rec.Code)
	}
}

func TestChatScenarioAccessWithAuthOff(t *testing.T) {
	s := accessFixture(t)
	s.AuthManager.enabled = false
	if code, out := getAccess(t, s, root, "phil"); code != http.StatusOK || out.AuthEnabled || len(out.Users) != 0 || len(out.Collections) != 2 {
		t.Errorf("disabled: %d %+v", code, out)
	}
	s.AuthManager = nil
	rec := chatREST(t, s, nil, http.MethodGet, "/v1/chat/scenarios/phil/access", "")
	if rec.Code != http.StatusOK || rec.Body.String() != `{"scenario":"phil","authEnabled":false,"collections":["docs","private"],"users":[]}` {
		t.Errorf("no auth manager: %d %s", rec.Code, rec.Body.String())
	}
}

// grantLocked is CheckPermission's rule: the first group by name wins, a
// group's own grant before its "*" grant, and nothing matches a write here.
func TestGrantLockedOrder(t *testing.T) {
	s := accessFixture(t)
	am := s.AuthManager
	am.mu.RLock()
	defer am.mu.RUnlock()
	for _, c := range []struct{ user, coll, want string }{
		{"carl", "docs", "group:all:*"}, // "all" < "zeta"
		{"bob", "docs", "group:readers"},
		{"bob", "private", ""},
		{"alice", "docs", "user"},
	} {
		if got := am.grantLocked(c.user, c.coll, PermRead); got != c.want {
			t.Errorf("%s on %s: %q, want %q", c.user, c.coll, got, c.want)
		}
	}
	if got := am.grantLocked("bob", "docs", PermWrite); got != "" {
		t.Errorf("bob write: %q", got)
	}
}
