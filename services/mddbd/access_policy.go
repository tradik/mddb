package main

import (
	"context"
	"strings"
)

// The access gate (SEC-019): one rule per entry point, enforced before the
// entry point runs, on every surface.
//
// Permissions used to be checked by each handler on its own, and a survey
// found REST routes, gRPC methods, MCP tools and GraphQL fields that took a
// collection and never checked it — a key confined to one collection, or to
// one tenant, read and wrote everyone's. Now every entry point is listed in a
// table (access_rules_*.go), the table says what it needs, and a gate in
// front of each surface applies authorize to it. An entry point missing from
// its table is refused while auth is on, and the completeness tests fail, so
// a new one cannot ship unclassified.

// accessClass is what kind of authority an entry point needs.
type accessClass uint8

const (
	// accessUnclassified is the zero value: an entry point nobody classified.
	// It is refused, so a missing table entry fails closed.
	accessUnclassified accessClass = iota
	// accessPublic needs no credentials (health probes, login).
	accessPublic
	// accessAuthenticated needs a valid credential and touches no collection
	// data (server info, stateless helpers, a caller's own account).
	accessAuthenticated
	// accessAdmin needs a global administrator.
	accessAdmin
	// accessCollection needs the rule's permission on every collection the
	// request names.
	accessCollection
	// accessHandler is authorized inside the entry point, which alone knows
	// the collection: a job or rule looked up by id, a multipart form, a
	// protocol carried inside the request (MCP JSON-RPC, GraphQL), an event
	// stream filtered per event, replication's own credentials. Each is
	// listed with the reason, and the behaviour tests cover them.
	accessHandler
)

// accessRule is how one entry point is authorized.
type accessRule struct {
	class accessClass
	// op is the permission a collection rule needs.
	op PermissionType
	// methods overrides the rule per HTTP method, for routes whose methods
	// read and write (GET lists, POST adds, DELETE removes).
	methods map[string]accessRule
	// all marks an entry point that acts on every collection whatever the
	// request names (server-wide statistics, every webhook): it needs the
	// permission on "*", and a collection in the request cannot narrow that.
	all bool
	// tenantFiltered marks an entry point that, acting on every collection,
	// filters its answer to the caller's tenant. Without it a tenant user is
	// refused, because the answer would show every tenant's data.
	tenantFiltered bool
	// fixed are collections the entry point always touches, whatever the
	// request names (the memory store).
	fixed []string
	// fallback are the collections used when the request names none (the
	// reference tools default to every reference collection).
	fallback []string
}

// Rule shorthands the tables are written in.
var (
	rulePublic        = accessRule{class: accessPublic}
	ruleAuthenticated = accessRule{class: accessAuthenticated}
	ruleAdmin         = accessRule{class: accessAdmin}
	ruleHandler       = accessRule{class: accessHandler}
	ruleRead          = accessRule{class: accessCollection, op: PermRead}
	ruleWrite         = accessRule{class: accessCollection, op: PermWrite}
	// ruleListing reads the named collection, or every collection of the
	// caller's tenant when none is named.
	ruleListing = accessRule{class: accessCollection, op: PermRead, tenantFiltered: true}
	// ruleAllRead and ruleAllWrite act on every collection, unfiltered.
	ruleAllRead  = accessRule{class: accessCollection, op: PermRead, all: true}
	ruleAllWrite = accessRule{class: accessCollection, op: PermWrite, all: true}
	// ruleAllListing acts on every collection and filters to the tenant.
	ruleAllListing = accessRule{class: accessCollection, op: PermRead, all: true, tenantFiltered: true}
	// ruleReadWrite reads on GET and writes on every other method.
	ruleReadWrite = accessRule{class: accessCollection, op: PermWrite,
		methods: map[string]accessRule{"GET": ruleRead, "HEAD": ruleRead}}
)

// forMethod is the rule a request with this HTTP method is held to.
func (r accessRule) forMethod(method string) accessRule {
	if m, ok := r.methods[method]; ok {
		return m
	}
	return r
}

// authOn reports whether permissions are enforced at all. With auth off every
// gate lets everything through, exactly as before the gate existed.
func (s *Server) authOn() bool {
	return s.AuthManager != nil && s.AuthManager.enabled
}

// authorize applies a rule to a request that names the given collections. It
// returns nil, ErrUnauthorized or ErrForbidden.
func (s *Server) authorize(ctx context.Context, rule accessRule, names []string) error {
	if !s.authOn() {
		return nil
	}
	am := s.AuthManager
	switch rule.class {
	case accessPublic, accessHandler:
		return nil
	case accessAuthenticated:
		if _, ok := GetClaimsFromContext(ctx); !ok {
			return ErrUnauthorized
		}
		return nil
	case accessAdmin:
		return am.CheckPermission(ctx, "*", PermAdmin)
	case accessCollection:
	default:
		return ErrForbidden
	}
	if rule.all {
		names = nil
	}
	colls := s.expandCollections(append(append([]string(nil), names...), rule.fixed...))
	if len(colls) == 0 {
		colls = s.expandCollections(rule.fallback)
	}
	if len(colls) == 0 {
		// Every collection: a tenant may ask only where the answer is
		// filtered to its own.
		if !rule.tenantFiltered && TenantFromContext(ctx) != "" {
			return ErrForbidden
		}
		return am.CheckPermission(ctx, "*", rule.op)
	}
	for _, c := range colls {
		if err := am.CheckPermission(ctx, c, rule.op); err != nil {
			return err
		}
	}
	return nil
}

// expandCollections drops empty names and turns a "role:<role>" target into
// the collections that hold the role, the way cross-collection search does.
func (s *Server) expandCollections(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		switch {
		case n == "":
		case strings.HasPrefix(n, rolePrefix):
			out = append(out, s.expandTargets([]string{n})...)
		default:
			out = append(out, n)
		}
	}
	return out
}

// collectionsFromArgs reads the collection fields of a decoded request — an
// MCP tool's arguments, a GraphQL argument map — in both spellings: the same
// fields alias resolution rewrites.
func collectionsFromArgs(args map[string]any) []string {
	var out []string
	for _, f := range aliasStringFields {
		if v, ok := args[f].(string); ok {
			out = append(out, v)
		}
	}
	for _, f := range aliasListFields {
		switch list := args[f].(type) {
		case []any:
			for _, item := range list {
				if v, ok := item.(string); ok {
					out = append(out, v)
				}
			}
		case []string:
			out = append(out, list...)
		}
	}
	return out
}
