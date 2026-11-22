package main

import (
	"cmp"
	"net/http"
	"slices"
)

// GET /v1/chat/scenarios/{name}/access (#441): who can read a scenario.
//
// A scenario is offered only to an account that can read every collection it
// allows (chatScenarioReadable), and mddb-chat sees it only through the
// account it logs in as. This route lists every user with the rule that lets
// them read each of those collections, so an administrator does not compare
// grants by hand. It is admin on "*" only and changes nothing.

// chatScenarioAccess is the body of the route.
type chatScenarioAccess struct {
	Scenario    string               `json:"scenario"`
	AuthEnabled bool                 `json:"authEnabled"`
	Collections []string             `json:"collections"`
	Users       []chatScenarioReader `json:"users"`
}

// chatScenarioReader is one user: Full when they can read every collection
// of the scenario, and one grant per collection they can read.
type chatScenarioReader struct {
	Username string              `json:"username"`
	Admin    bool                `json:"admin"`
	Disabled bool                `json:"disabled,omitempty"`
	Full     bool                `json:"full"`
	Grants   []chatScenarioGrant `json:"grants"`
}

// chatScenarioGrant names the rule that gives read on one collection: admin,
// user, user:*, group:<name> or group:<name>:*.
type chatScenarioGrant struct {
	Collection string `json:"collection"`
	Via        string `json:"via"`
}

// getChatScenarioAccess answers the route: 403 before 404, so a caller who
// is not an administrator learns nothing about which scenarios exist.
func (s *Server) getChatScenarioAccess(w http.ResponseWriter, r *http.Request, name string) {
	if !s.requireAdmin(w, r) {
		return
	}
	sc, err := s.GetChatScenario(r.Context(), name)
	if err != nil {
		chatScenarioError(w, err)
		return
	}
	out := chatScenarioAccess{Scenario: sc.Name, Collections: make([]string, 0, len(sc.AllowedCollections)), Users: []chatScenarioReader{}}
	for _, c := range sc.AllowedCollections {
		out.Collections = append(out.Collections, s.Aliases.Resolve(c))
	}
	if s.AuthManager != nil && s.AuthManager.IsEnabled() {
		out.AuthEnabled = true
		out.Users = s.AuthManager.scenarioReaders(out.Collections)
	}
	ok(w, out)
}

// scenarioReaders lists every user, by name, with what they can read of
// collections. It follows CheckPermission for a login token of that user:
// the tenant gate, then admin on "*", then grantLocked.
func (am *AuthManager) scenarioReaders(collections []string) []chatScenarioReader {
	am.mu.RLock()
	defer am.mu.RUnlock()
	out := make([]chatScenarioReader, 0, len(am.users))
	for _, u := range am.users {
		admin := u.Tenant == "" && am.isAdminLocked(u.Username)
		reader := chatScenarioReader{Username: u.Username, Admin: admin, Disabled: u.Disabled, Grants: []chatScenarioGrant{}}
		for _, c := range collections {
			if via := am.readGrantLocked(u, admin, c); via != "" {
				reader.Grants = append(reader.Grants, chatScenarioGrant{Collection: c, Via: via})
			}
		}
		reader.Full = len(reader.Grants) == len(collections)
		out = append(out, reader)
	}
	slices.SortFunc(out, func(a, b chatScenarioReader) int { return cmp.Compare(a.Username, b.Username) })
	return out
}

// readGrantLocked is the rule that lets u read collection, or "". A tenant
// user reads only inside their namespace and is never a global admin
// (GenerateTenantJWT never sets the flag). The caller holds am.mu.
func (am *AuthManager) readGrantLocked(u *User, admin bool, collection string) string {
	if u.Tenant != "" && !CollectionInTenant(u.Tenant, collection) {
		return ""
	}
	if admin {
		return "admin"
	}
	return am.grantLocked(u.Username, collection, PermRead)
}
