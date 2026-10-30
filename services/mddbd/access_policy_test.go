package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	"mddb/internal/automationlog"
	"mddb/internal/spell"
	"mddb/internal/temporal"
	proto "mddb/proto"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	protov2 "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// SEC-019: every entry point of every surface is classified, and the
// classification means what it says for every principal.

// recordingMux remembers the patterns registered on it.
type recordingMux struct{ patterns map[string]http.Handler }

func (m *recordingMux) Handle(p string, h http.Handler) { m.patterns[p] = h }
func (m *recordingMux) HandleFunc(p string, h func(http.ResponseWriter, *http.Request)) {
	m.Handle(p, http.HandlerFunc(h))
}

// allRoutes registers every route the server can serve, every optional
// subsystem on, on a recorder.
func allRoutes(t *testing.T) map[string]http.Handler {
	t.Helper()
	t.Setenv("MDDB_PPROF_ENABLED", "true")
	t.Setenv(esCompatEnv, "true")
	t.Setenv("MDDB_GRAPHQL_ENABLED", "true")
	t.Setenv("MDDB_GRAPHQL_PLAYGROUND", "true")
	s := accessServer(t)
	s.AutomationManager = NewAutomationManager(s.DB)
	s.AutomationLogStore = automationlog.NewStore(s.DB, time.Hour)
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	s.SpellManager = spell.NewSpellManager(s.DB)
	rec := &recordingMux{patterns: map[string]http.Handler{}}
	s.registerRoutes(rec, true)
	s.registerGraphQL(rec, true)
	s.registerMCPRoutes(rec, nil, nil)
	return rec.patterns
}

func TestEveryHTTPRouteIsClassified(t *testing.T) {
	routes := allRoutes(t)
	for p := range routes {
		if httpAccessRules[p].class == accessUnclassified {
			t.Errorf("route %s has no rule in httpAccessRules", p)
		}
	}
	for p := range httpAccessRules {
		if _, ok := routes[p]; !ok {
			t.Errorf("httpAccessRules names %s, which no listener registers", p)
		}
	}
}

func TestEveryGRPCMethodIsClassified(t *testing.T) {
	seen := map[string]bool{}
	services := proto.File_mddb_proto.Services()
	for i := 0; i < services.Len(); i++ {
		sd := services.Get(i)
		for j := 0; j < sd.Methods().Len(); j++ {
			name := "/" + string(sd.FullName()) + "/" + string(sd.Methods().Get(j).Name())
			seen[name] = true
			if grpcAccessRules[name].class == accessUnclassified {
				t.Errorf("RPC %s has no rule in grpcAccessRules", name)
			}
		}
	}
	for name := range grpcAccessRules {
		if !seen[name] && !strings.HasPrefix(name, "/grpc.reflection.") {
			t.Errorf("grpcAccessRules names %s, which no service has", name)
		}
	}
}

func TestEveryMCPToolIsClassified(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range mcpBuiltinTools() {
		seen[tool.Name] = true
		if mcpToolAccessRules[tool.Name].class == accessUnclassified {
			t.Errorf("MCP tool %s has no rule in mcpToolAccessRules", tool.Name)
		}
	}
	for name := range mcpToolAccessRules {
		if !seen[name] {
			t.Errorf("mcpToolAccessRules names %s, which is not a tool", name)
		}
	}
}

func TestEveryGraphQLFieldIsClassified(t *testing.T) {
	schema := gql.NewExecutableSchema(gql.Config{Resolvers: gql.NewResolver(nil)}).Schema()
	seen := map[string]bool{}
	for _, root := range []*ast.Definition{schema.Query, schema.Mutation, schema.Subscription} {
		if root == nil {
			continue
		}
		for _, f := range root.Fields {
			if strings.HasPrefix(f.Name, "__") {
				continue
			}
			key := root.Name + "." + f.Name
			seen[key] = true
			if graphqlAccessRules[key].class == accessUnclassified {
				t.Errorf("GraphQL field %s has no rule in graphqlAccessRules", key)
			}
		}
	}
	for key := range graphqlAccessRules {
		if !seen[key] {
			t.Errorf("graphqlAccessRules names %s, which the schema does not have", key)
		}
	}
}

// expectAllowed is what a rule promises a principal on a request naming coll
// ("" for none). The fixture: alice may read and write "docs"; tina is tenant
// t1 with "*" read and write; root is a global administrator.
func expectAllowed(rule accessRule, who *JWTClaims, coll string) bool {
	switch rule.class {
	case accessPublic, accessHandler, accessAuthenticated:
		return true
	case accessAdmin:
		return who.Admin
	case accessUnclassified:
		return false
	}
	if who.Admin {
		return true
	}
	if rule.op == PermAdmin {
		return false
	}
	named := []string{coll}
	if rule.all || coll == "" {
		named = nil
	}
	named = append(named, rule.fixed...)
	if len(named) == 0 && len(rule.fallback) > 0 {
		return false // no reference collection exists in the fixture: "*" it is
	}
	if who == alice {
		return len(named) == 1 && named[0] == "docs"
	}
	if len(named) == 0 {
		return rule.tenantFiltered && rule.op == PermRead
	}
	for _, c := range named {
		if !CollectionInTenant(who.Tenant, c) {
			return false
		}
	}
	return true
}

// accessCases are the principal and collection pairs every rule is tried on.
var accessCases = []struct {
	who  *JWTClaims
	coll string
}{
	{alice, "docs"}, {alice, "secret"}, {alice, ""},
	{tina, "t1/notes"}, {tina, "t2/notes"}, {tina, "docs"}, {tina, ""},
	{root, "secret"}, {root, ""},
}

func ruleKeys(m map[string]accessRule) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ruleMethods is POST followed by every method a rule overrides.
func ruleMethods(base accessRule) []string {
	methods := []string{http.MethodPost}
	for m := range base.methods {
		methods = append(methods, m)
	}
	return methods
}

// gateRequest is a request to pattern naming coll (when set) both in the
// query string and in the JSON body.
func gateRequest(method, pattern, coll string) *http.Request {
	target := pattern
	body := "{}"
	if coll != "" {
		target += "?collection=" + coll
		body = `{"collection":"` + coll + `"}`
	}
	return httptest.NewRequest(method, target, strings.NewReader(body))
}

func TestHTTPGateAppliesEveryRule(t *testing.T) {
	s := accessServer(t)
	for _, pattern := range ruleKeys(httpAccessRules) {
		base := httpAccessRules[pattern]
		for _, method := range ruleMethods(base) {
			rule := base.forMethod(method)
			for _, c := range accessCases {
				reached := false
				h := s.httpGate(pattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
				req := gateRequest(method, pattern, c.coll)
				rec := httptest.NewRecorder()
				asUser(c.who, h).ServeHTTP(rec, req)
				if want := expectAllowed(rule, c.who, c.coll); reached != want {
					t.Errorf("%s %s as %s on %q: reached=%v, want %v (status %d)",
						method, pattern, c.who.Username, c.coll, reached, want, rec.Code)
				}
			}
		}
	}
}

// grpcRequest builds the request message of a method, naming coll when the
// message has a collection field.
func grpcRequest(t *testing.T, method string) (protov2.Message, protoreflect.FieldDescriptor) {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(method, "/"), "/")
	sd, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(parts[0]))
	if err != nil {
		t.Fatal(err)
	}
	md := sd.(protoreflect.ServiceDescriptor).Methods().ByName(protoreflect.Name(parts[1]))
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	if err != nil {
		t.Fatal(err)
	}
	msg := mt.New().Interface()
	return msg, msg.ProtoReflect().Descriptor().Fields().ByName("collection")
}

func TestGRPCGateAppliesEveryRule(t *testing.T) {
	s := accessServer(t)
	for _, method := range ruleKeys(grpcAccessRules) {
		if strings.HasPrefix(method, "/grpc.reflection.") {
			continue
		}
		rule := grpcAccessRules[method]
		for _, c := range accessCases {
			msg, fd := grpcRequest(t, method)
			coll := c.coll
			if fd == nil {
				coll = ""
			} else if coll != "" {
				msg.ProtoReflect().Set(fd, protoreflect.ValueOfString(coll))
			}
			ctx := context.WithValue(context.Background(), authContextKey, c.who)
			err := s.authorizeRPC(ctx, method, msg)
			if want := expectAllowed(rule, c.who, coll); (err == nil) != want {
				t.Errorf("%s as %s on %q: %v, want allowed=%v", method, c.who.Username, coll, err, want)
			}
		}
	}
}

func TestMCPGateAppliesEveryRule(t *testing.T) {
	s := accessServer(t)
	for _, tool := range ruleKeys(mcpToolAccessRules) {
		rule := mcpToolAccessRules[tool]
		for _, c := range accessCases {
			args := map[string]any{}
			if c.coll != "" {
				args["collection"] = c.coll
			}
			ctx := context.WithValue(context.Background(), authContextKey, c.who)
			err := s.authorizeTool(ctx, tool, args)
			if want := expectAllowed(rule, c.who, c.coll); (err == nil) != want {
				t.Errorf("%s as %s on %q: %v, want allowed=%v", tool, c.who.Username, c.coll, err, want)
			}
		}
	}
}

func TestGraphQLGateAppliesEveryRule(t *testing.T) {
	s := accessServer(t)
	for _, key := range ruleKeys(graphqlAccessRules) {
		rule := graphqlAccessRules[key]
		object, field, _ := strings.Cut(key, ".")
		for _, c := range accessCases {
			args := map[string]any{}
			if c.coll != "" {
				args["collection"] = c.coll
			}
			fc := &graphql.FieldContext{Object: object, Args: args,
				Field: graphql.CollectedField{Field: &ast.Field{Name: field}}}
			ctx := graphql.WithFieldContext(context.WithValue(context.Background(), authContextKey, c.who), fc)
			_, err := s.accessFieldMiddleware(ctx, func(context.Context) (any, error) { return true, nil })
			if want := expectAllowed(rule, c.who, c.coll); (err == nil) != want {
				t.Errorf("%s as %s on %q: %v, want allowed=%v", key, c.who.Username, c.coll, err, want)
			}
		}
	}
}
