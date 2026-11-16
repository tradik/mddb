package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Routing REST requests across groups (GO-052). Every route is registered
// behind placementRoute, after the access gate: a request is authorised
// where it arrives, then served here or sent where it belongs.
//
//   - A request naming collections goes to the group holding them: a read is
//     served by any node of that group (this one, when it is in it), a write
//     by the group's leader. A write held by a fence waits here.
//   - Cross-search over collections of several groups, and /v1/stats, ask
//     every group and merge the answers (placement_fanout*.go).
//   - A write to a global setting goes to the meta group's leader, and so
//     does everything under /v1/placement.
//   - Everything else is this node's own business.

// Headers between nodes.
const (
	hdrPlacementHops    = "X-MDDB-Placement-Hops"    // how many nodes sent the request on
	hdrPlacementPartial = "X-MDDB-Placement-Partial" // a fan-out's part: answer from this group only
	hdrPlacementGroup   = "X-MDDB-Group"             // the group a redirect names
)

// metaRoutes go to the meta group's leader whatever the method.
var metaRoutes = map[string]bool{"/v1/placement": true, "/v1/placement/move": true}

// globalWriteRoutes write global settings: anything but GET goes to the
// meta group's leader.
var globalWriteRoutes = map[string]bool{
	"/v1/auth/register": true, "/v1/auth/users": true, "/v1/auth/users/": true,
	"/v1/auth/groups": true, "/v1/auth/groups/": true, "/v1/auth/group-permissions": true,
	"/v1/auth/permissions": true, "/v1/auth/api-key": true, "/v1/auth/api-keys": true, "/v1/auth/api-keys/": true,
	"/v1/mcp/keys": true, "/v1/mcp/keys/disable": true, "/v1/tenants": true, "/v1/tenants/": true,
	"/v1/features/": true, "/v1/aliases": true, "/v1/aliases/": true,
	"/v1/webhooks": true, "/v1/webhooks/delete": true, "/v1/automation": true, "/v1/automation/": true,
	"/v1/embedding-configs": true, "/v1/embedding-configs/": true, "/v1/embedding-configs/set-default": true,
	"/v1/chat/skills": true, "/v1/chat/skills/": true, "/v1/chat/mcp-secrets": true, "/v1/chat/mcp-secrets/": true,
	"/v1/chat/scenarios/": true,
}

// maxRouteRounds bounds how often one request is routed again because the
// map changed while it waited.
const maxRouteRounds = 3

// placementRoute puts the router in front of the handler of pattern; with no
// placement it is the handler itself.
func (s *Server) placementRoute(pattern string, next http.Handler) http.Handler {
	p := s.placement
	if p == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.route(w, r, pattern, next) })
}

// route sends one request where it belongs.
func (p *placementRuntime) route(w http.ResponseWriter, r *http.Request, pattern string, next http.Handler) {
	switch {
	case r.Header.Get(hdrPlacementPartial) != "":
		next.ServeHTTP(w, r) // a part of another node's fan-out
	case metaRoutes[pattern] || (globalWriteRoutes[pattern] && r.Method != http.MethodGet):
		p.toLeader(w, r, p.cfg.meta, next)
	case pattern == "/v1/stats":
		p.fanoutStats(w, r, next)
	default:
		p.routeCollections(w, r, pattern, next)
	}
}

// routeCollections routes a request by the collections it names.
func (p *placementRuntime) routeCollections(w http.ResponseWriter, r *http.Request, pattern string, next http.Handler) {
	rule := httpAccessRules[pattern].forMethod(r.Method)
	if rule.class != accessCollection || rule.all {
		next.ServeHTTP(w, r)
		return
	}
	names, err := requestCollections(r)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	names = p.resolveNames(append(names, rule.fixed...))
	for round := 0; round < maxRouteRounds; round++ {
		groups, known := p.groupsOf(names)
		switch {
		case !known:
			writePlacementBusy(w, "this node has not received the placement map yet")
			return
		case pattern == "/v1/cross-search" && (len(groups) > 1 || hasRoleTarget(names)):
			p.fanoutCross(w, r, next)
			return
		case len(groups) > 1:
			writeJSONStatus(w, http.StatusConflict, map[string]any{"error": "the request names collections held by different node groups",
				"groups": groups})
			return
		case len(groups) == 0:
			next.ServeHTTP(w, r)
			return
		}
		if !p.serveInGroup(w, r, groupName(groups), names, rule.op == PermWrite, next) {
			continue // the map changed while the write waited
		}
		return
	}
	writePlacementBusy(w, "the collection's placement keeps changing")
}

// serveInGroup serves a request for collections of group, or sends it on;
// false means it must be routed again.
func (p *placementRuntime) serveInGroup(w http.ResponseWriter, r *http.Request, group string, names []string, write bool, next http.Handler) bool {
	switch {
	case group != p.cfg.self:
		p.forward(w, r, group)
		return true
	case !write:
		next.ServeHTTP(w, r)
		return true
	case !p.leads():
		p.toLeader(w, r, group, next)
		return true
	}
	var ends []func()
	defer func() {
		for _, end := range ends {
			end()
		}
	}()
	for _, c := range names {
		end, again, err := p.enterWrite(r.Context(), c)
		if again {
			return false
		}
		if err != nil {
			writePlacementBusy(w, err.Error())
			return true
		}
		ends = append(ends, end)
	}
	next.ServeHTTP(w, r)
	return true
}

// toLeader serves the request here when this node leads group, and sends
// it to group's leader otherwise. A node that leads its group but has
// briefly lost its lease — a heartbeat late under load — waits for it, up
// to the fence timeout, rather than sending the request to itself. With no
// known leader of its own group the request is served here too, where the
// write guards answer as without placement.
func (p *placementRuntime) toLeader(w http.ResponseWriter, r *http.Request, group string, next http.Handler) {
	if group == p.cfg.self {
		leader := p.leaderOf(group)
		if leader == p.s.NodeID {
			p.awaitLease(r.Context())
		}
		if leader == "" || leader == p.s.NodeID || p.leads() {
			next.ServeHTTP(w, r)
			return
		}
	}
	p.forward(w, r, group)
}

// leaseWait is how often awaitLease looks again.
const leaseWait = 5 * time.Millisecond

// awaitLease waits until this node may write, at most the fence timeout.
func (p *placementRuntime) awaitLease(ctx context.Context) {
	deadline := time.Now().Add(p.cfg.fenceTimeout)
	for !p.leads() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(leaseWait)
	}
}

// resolveNames is names without empties and duplicates, aliases resolved.
func (p *placementRuntime) resolveNames(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if p.s.Aliases != nil && !strings.HasPrefix(n, rolePrefix) {
			n = p.s.Aliases.Resolve(n)
		}
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// groupsOf groups names by the group holding each; role targets belong to
// no group. known is false while the node has no map.
func (p *placementRuntime) groupsOf(names []string) (map[string][]string, bool) {
	out := map[string][]string{}
	for _, n := range names {
		if strings.HasPrefix(n, rolePrefix) {
			continue
		}
		g, known := p.owner(n)
		if !known {
			return nil, false
		}
		out[g] = append(out[g], n)
	}
	return out, true
}

// groupName is the one group of a single-group map.
func groupName(groups map[string][]string) string {
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	return names[0]
}

// hasRoleTarget reports a "role:" target, which every group may hold.
func hasRoleTarget(names []string) bool {
	for _, n := range names {
		if strings.HasPrefix(n, rolePrefix) {
			return true
		}
	}
	return false
}

// writePlacementBusy answers a request the placement cannot serve now; the
// client may retry.
func writePlacementBusy(w http.ResponseWriter, why string) {
	w.Header().Set("Retry-After", "1")
	writeJSONStatus(w, http.StatusServiceUnavailable, map[string]string{"error": why})
}

// errNoGroupLeader is a request for a group whose leader is not known.
var errNoGroupLeader = errors.New("the node group holding the collection has no known leader")
