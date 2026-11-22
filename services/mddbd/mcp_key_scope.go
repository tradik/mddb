package main

import (
	"context"
	"strings"
)

// MCP API key scope (#439): a key confined to collections, to reads, or to
// the reach of one chat scenario.
//
// A key used to be limited by toolsets only, so the one way to give a remote
// client a single collection was to turn MCP keys off and use a main-auth user
// per collection. A key now carries collections, readOnly and scenario; the
// key middleware puts them in the request's context (never read from a
// header), and authorizeMCP applies them to every tools/call and
// resources/read of a call that came in without main-auth claims.
//
// The scope is resolved when it is checked, not when the key is made: an alias
// or a "role:" target in collections reaches what it reaches now, and a bound
// scenario's allowedCollections are read from the stored scenario on every
// call, so editing the scenario changes what the key may do.
//
// Per rule class, a scoped key may call:
//   - public and authenticated rules (no collection data): yes;
//   - a collection rule: when every collection the call names (after alias
//     and role resolution) is in the scope; when it names none, only when the
//     rule's fallback targets resolve inside the scope; a write is refused to
//     a read-only key, as is any tool read-only mode would refuse, and to a
//     call naming one of readOnlyCollections (AGT-007);
//   - an "every collection" rule: only when collections are not limited;
//   - a memory rule: only to a limited key, inside its own memory namespace
//     (AGT-008, mcp_key_memory.go), and only the reads to a read-only key;
//   - admin and handler rules: never. A handler authorizes inside with
//     main-auth claims a key does not have.
//
// The memory collections are never in a scope's reach, so a limited key
// cannot read the store around its namespace with the document tools.

// mcpKeyScope is what a key is confined to, as stored or configured.
type mcpKeyScope struct {
	// collections are the names a key may reach — collections, aliases or
	// "role:" targets; empty = not limited by collection.
	collections []string
	// readOnly refuses every tool read-only mode refuses.
	readOnly bool
	// readOnlyCollections are names among collections the key may read but
	// not write (AGT-007), resolved as collections are.
	readOnlyCollections []string
	// scenario binds the key to a chat scenario: its allowedCollections,
	// always read-only, intersected with collections when both are set.
	scenario string
	// memoryNamespace is the part of the memory store a limited key reads
	// and writes (AGT-008): derived from the key, or the key's explicit
	// memoryNamespace. It does not make a key scoped.
	memoryNamespace string
}

// scoped reports whether the scope limits anything: a key without any of the
// three behaves as before #439.
func (k mcpKeyScope) scoped() bool { return len(k.collections) > 0 || k.readOnly || k.scenario != "" }

// limited reports whether the key reaches only some collections.
func (k mcpKeyScope) limited() bool { return len(k.collections) > 0 || k.scenario != "" }

// reads reports whether the key may only read.
func (k mcpKeyScope) reads() bool { return k.readOnly || k.scenario != "" }

type mcpKeyScopeContextKey struct{}

// withKeyScope records the scope of the MCP API key that authenticated the
// request. Set by the key middleware only; an unscoped key leaves no value.
func withKeyScope(ctx context.Context, k mcpKeyScope) context.Context {
	if !k.scoped() {
		return ctx
	}
	return context.WithValue(ctx, mcpKeyScopeContextKey{}, k)
}

// keyScopeFromContext is the scope of the request's key, for a call that
// carries no main-auth claims: those are authorized by RBAC instead.
func keyScopeFromContext(ctx context.Context) (mcpKeyScope, bool) {
	if _, claims := GetClaimsFromContext(ctx); claims {
		return mcpKeyScope{}, false
	}
	k, ok := ctx.Value(mcpKeyScopeContextKey{}).(mcpKeyScope)
	return k, ok
}

// keyScopeMayCall is the verdict that depends only on the rule and the tool,
// not on the collections a call names: what tools/list leaves out, and the
// first check of every call. tool is "" for a resource.
func keyScopeMayCall(k mcpKeyScope, tool string, rule accessRule) bool {
	switch rule.class {
	case accessPublic, accessAuthenticated:
		return !k.reads() || tool == "" || toolIsReadOnly(tool)
	case accessCollection:
	default:
		return false // admin, handler, unclassified
	}
	switch {
	case len(rule.fixed) > 0: // the memory store: a limited key's own namespace
		return k.limited() && k.memoryNamespace != "" && (rule.op == PermRead || !k.reads())
	case k.reads() && keyScopeWrites(tool, rule):
		return false
	case rule.all && k.limited():
		return false
	}
	return true
}

// keyScopeRefreshes are the tools that write only a mirror of an outside
// source, through a sync rule an administrator made (INT-023). A read-only
// key keeps a collection's documents safe from the agent; asking for the
// mirror to be brought up to date is not a write of the agent's, and the
// recommended project key holds its issues collection read-only. The
// collection must still be in the key's scope, and the tool limits itself to
// one run per rule every 30 s.
var keyScopeRefreshes = map[string]bool{"sync_refresh": true}

// keyScopeWrites reports whether a call of tool under rule counts as a write
// for a key's scope: a write rule, or a tool read-only mode would refuse.
func keyScopeWrites(tool string, rule accessRule) bool {
	if keyScopeRefreshes[tool] {
		return false
	}
	return rule.op != PermRead || (tool != "" && !toolIsReadOnly(tool))
}

// toolIsReadOnly reports whether a built-in tool is one read-only mode lets
// through: the same annotation decides for a read-only key.
func toolIsReadOnly(name string) bool {
	ann, ok := mcpToolAnnotations[name]
	return ok && ann.ReadOnlyHint != nil && *ann.ReadOnlyHint
}

// keyScopeMayList reports whether tools/list shows a built-in tool to the
// request's key: a tool it can never call is left out.
func keyScopeMayList(ctx context.Context, tool string) bool {
	k, ok := keyScopeFromContext(ctx)
	return !ok || keyScopeMayCall(k, tool, mcpToolAccessRules[tool])
}

// authorizeKeyScope applies the request's key scope to a call of rule on the
// named collections: nil when there is no scope to apply, else nil or
// ErrForbidden.
func (s *Server) authorizeKeyScope(ctx context.Context, tool string, rule accessRule, names []string) error {
	k, ok := keyScopeFromContext(ctx)
	if !ok {
		return nil
	}
	reach, err := s.keyScopeReach(k)
	if err != nil || !keyScopeMayCall(k, tool, rule) {
		return ErrForbidden
	}
	if rule.class != accessCollection || !k.limited() || len(rule.fixed) > 0 {
		return nil // the memory tools confine a limited key to its namespace
	}
	colls := s.expandCollections(s.resolveScopeNames(names))
	if len(colls) == 0 {
		colls = s.expandCollections(rule.fallback)
	}
	if len(colls) == 0 {
		return ErrForbidden // every collection
	}
	var readOnly map[string]bool
	if keyScopeWrites(tool, rule) {
		readOnly = s.collectionSet(k.readOnlyCollections)
	}
	for _, c := range colls {
		if !reach[c] || readOnly[c] {
			return ErrForbidden
		}
	}
	return nil
}

// keyScopeReads reports whether the request's key may read every one of
// names (nil when it has no scope): the check a listing filters with.
func (s *Server) keyScopeReads(ctx context.Context, names []string) error {
	return s.authorizeKeyScope(ctx, "", ruleRead, names)
}

// keyScopeReach is the set of collections a scope reaches now. A scenario
// that cannot be served — gone, disabled, or chat off — reaches nothing and
// is an error, so the key's every call is refused, as mddb-chat refuses to
// join it.
func (s *Server) keyScopeReach(k mcpKeyScope) (map[string]bool, error) {
	var reach map[string]bool
	if k.scenario != "" {
		sc, err := s.boundScenario(k.scenario)
		if err != nil {
			return nil, err
		}
		reach = s.collectionSet(sc.AllowedCollections)
	}
	if len(k.collections) > 0 {
		own := s.collectionSet(k.collections)
		if reach == nil {
			reach = own
		}
		for c := range reach {
			if !own[c] {
				delete(reach, c)
			}
		}
	}
	for _, c := range memoryCollections {
		delete(reach, c) // reached through the namespace only (AGT-008)
	}
	return reach, nil
}

// collectionSet resolves names — aliases, then "role:" targets — to the
// collections they reach now.
func (s *Server) collectionSet(names []string) map[string]bool {
	set := map[string]bool{}
	for _, c := range s.expandCollections(s.resolveScopeNames(names)) {
		set[c] = true
	}
	return set
}

// resolveScopeNames turns each alias into its collection; other names, and
// "role:" targets, are left for expandCollections.
func (s *Server) resolveScopeNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !strings.HasPrefix(n, rolePrefix) {
			n = s.Aliases.Resolve(n)
		}
		out = append(out, n)
	}
	return out
}

// boundScenario is the scenario a key is bound to, when it may be served:
// the chat module on, the scenario stored and enabled. Otherwise
// errChatScenarioNotFound, or ErrForbidden for a disabled one.
func (s *Server) boundScenario(name string) (*ChatScenario, error) {
	if s.ChatScenarios == nil || !s.featureEnabled(FeatureChat) {
		return nil, errChatScenariosUnavailable
	}
	sc, ok := s.ChatScenarios.Get(name)
	switch {
	case !ok:
		return nil, errChatScenarioNotFound
	case !sc.Enabled:
		return nil, ErrForbidden
	}
	return sc, nil
}

// keyScenarioInstructions is the composed prompt of the request's bound
// scenario — what /v1/chat/scenarios/{name}/prompt returns — when there is a
// scenario that may be served. ok is false otherwise, and the caller keeps
// the server's own instructions: a disabled scenario's prompt is not shown.
func (s *Server) keyScenarioInstructions(ctx context.Context) (string, bool) {
	k, ok := keyScopeFromContext(ctx)
	if !ok || k.scenario == "" {
		return "", false
	}
	sc, err := s.boundScenario(k.scenario)
	if err != nil {
		return "", false
	}
	return s.ChatScenarioPrompt(sc).Prompt, true
}
