package graphql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// covFBackendError is a comparable error type, so a constant can serve as the
// sentinel errors.Is looks for.
type covFBackendError string

func (e covFBackendError) Error() string { return string(e) }

// covFErrBackend is what covFServer returns when a test asks it to fail.
const covFErrBackend = covFBackendError("covF: backend failed")

// covFServer records every call a resolver makes and answers with a value
// unique to the method, so a test can tell that the resolver called the right
// method, with the right arguments, and handed back exactly what it got.
// Methods it does not override fall through to stubServer.
type covFServer struct {
	stubServer
	fail  bool
	calls []string
	last  any
}

// covFReply records the call and returns (v, nil), or (zero, error) when the
// server was told to fail.
func covFReply[T any](s *covFServer, v T, name string, args ...any) (T, error) {
	s.calls = append(s.calls, strings.TrimSpace(fmt.Sprintln(append([]any{name}, args...)...)))
	if s.fail {
		var zero T
		s.last = nil
		return zero, covFErrBackend
	}
	s.last = v
	return v, nil
}

// covFErr records the call for methods that only return an error.
func covFErr(s *covFServer, name string, args ...any) error {
	_, err := covFReply[any](s, nil, name, args...)
	return err
}

func (s *covFServer) AddDocument(_ context.Context, in AddDocumentInput) (*Document, error) {
	return covFReply(s, &Document{ID: "add"}, "AddDocument", in.Collection)
}
func (s *covFServer) UpdateDocument(_ context.Context, in UpdateDocumentInput) (*Document, error) {
	return covFReply(s, &Document{ID: "update"}, "UpdateDocument", in.Collection)
}
func (s *covFServer) DeleteDocument(_ context.Context, c, k, l string) error {
	return covFErr(s, "DeleteDocument", c, k, l)
}
func (s *covFServer) DeleteCollection(_ context.Context, c string) (int, error) {
	return covFReply(s, 7, "DeleteCollection", c)
}
func (s *covFServer) AddBatch(_ context.Context, c string, d []*AddBatchDocumentInput) (*BatchAddResult, error) {
	return covFReply(s, &BatchAddResult{Added: len(d)}, "AddBatch", c, len(d))
}
func (s *covFServer) IngestDocuments(_ context.Context, c string, d []*IngestDocumentInput, o *IngestOptionsInput) (*IngestResult, error) {
	return covFReply(s, &IngestResult{Collection: c}, "IngestDocuments", c, len(d), o != nil)
}
func (s *covFServer) VectorReindex(_ context.Context, c string, f *bool) (*VectorStats, error) {
	return covFReply(s, &VectorStats{Enabled: true}, "VectorReindex", c, *f)
}
func (s *covFServer) SetTTL(_ context.Context, c, k, l string, ttl int) (*Document, error) {
	return covFReply(s, &Document{ID: "ttl"}, "SetTTL", c, k, l, ttl)
}
func (s *covFServer) ImportURL(_ context.Context, c, u string, k *string, l string, m []*MetaInput, ttl *int) (*Document, error) {
	return covFReply(s, &Document{ID: "import"}, "ImportURL", c, u, *k, l, len(m), *ttl)
}
func (s *covFServer) RegisterWebhook(_ context.Context, in RegisterWebhookInput) (*Webhook, error) {
	return covFReply(s, &Webhook{ID: "wh"}, "RegisterWebhook", in.URL)
}
func (s *covFServer) DeleteWebhook(_ context.Context, id string) error {
	return covFErr(s, "DeleteWebhook", id)
}
func (s *covFServer) SetSchema(_ context.Context, in SetSchemaInput) error {
	return covFErr(s, "SetSchema", in.Collection)
}
func (s *covFServer) DeleteSchema(_ context.Context, c string) error {
	return covFErr(s, "DeleteSchema", c)
}
func (s *covFServer) ValidateDocument(_ context.Context, c string, m []*MetaInput) (*ValidationResult, error) {
	return covFReply(s, &ValidationResult{Valid: true}, "ValidateDocument", c, len(m))
}
func (s *covFServer) Register(_ context.Context, u, _ string) (*User, error) {
	return covFReply(s, &User{Username: u}, "Register", u)
}
func (s *covFServer) CreateAPIKey(_ context.Context, in CreateAPIKeyInput) (*APIKey, error) {
	return covFReply(s, &APIKey{Key: "k"}, "CreateAPIKey", in.Description)
}
func (s *covFServer) SetPermission(_ context.Context, in SetPermissionInput) error {
	return covFErr(s, "SetPermission", in.Username)
}
func (s *covFServer) CreateGroup(_ context.Context, in CreateGroupInput) (*Group, error) {
	return covFReply(s, &Group{Name: in.Name}, "CreateGroup", in.Name)
}
func (s *covFServer) UpdateGroup(_ context.Context, n, d string, m []string) (*Group, error) {
	return covFReply(s, &Group{Name: n}, "UpdateGroup", n, d, m)
}
func (s *covFServer) DeleteGroup(_ context.Context, n string) error {
	return covFErr(s, "DeleteGroup", n)
}
func (s *covFServer) SetGroupPermission(_ context.Context, in SetGroupPermissionInput) error {
	return covFErr(s, "SetGroupPermission", in.GroupName)
}
func (s *covFServer) GetDocument(_ context.Context, c, k, l string, env map[string]string) (*Document, error) {
	return covFReply(s, &Document{ID: "get"}, "GetDocument", c, k, l, env)
}
func (s *covFServer) SearchDocuments(_ context.Context, in SearchInput) (*DocumentConnection, error) {
	return covFReply(s, &DocumentConnection{TotalCount: 3}, "SearchDocuments", in.Collection)
}
func (s *covFServer) VectorSearch(_ context.Context, in VectorSearchInput) (*VectorSearchResponse, error) {
	return covFReply(s, &VectorSearchResponse{Total: 2}, "VectorSearch", in.Collection)
}
func (s *covFServer) VectorStats(_ context.Context) (*VectorStats, error) {
	return covFReply(s, &VectorStats{Enabled: true}, "VectorStats")
}
func (s *covFServer) FullTextSearch(_ context.Context, in FTSInput) (*FTSResponse, error) {
	return covFReply(s, &FTSResponse{Total: 4}, "FullTextSearch", in.Query)
}
func (s *covFServer) CodeGraph(_ context.Context, in CodeGraphInput) (*CodeGraph, error) {
	return covFReply(s, &CodeGraph{Root: in.Key}, "CodeGraph", in.Key)
}
func (s *covFServer) IndexingStatus(_ context.Context, c *string) (*IndexingStatus, error) {
	return covFReply(s, &IndexingStatus{Collection: c}, "IndexingStatus", *c)
}
func (s *covFServer) CodeSymbol(_ context.Context, in CodeSymbolInput) (*CodeSymbol, error) {
	return covFReply(s, &CodeSymbol{Symbol: in.Name}, "CodeSymbol", in.Collection, in.Name)
}
func (s *covFServer) GetStats(_ context.Context) (*Stats, error) {
	return covFReply(s, &Stats{Mode: "rw"}, "GetStats")
}
func (s *covFServer) ListWebhooks(_ context.Context, c *string) ([]*Webhook, error) {
	return covFReply(s, []*Webhook{{ID: "w1"}}, "ListWebhooks", *c)
}
func (s *covFServer) GetSchema(_ context.Context, c string) (*Schema, error) {
	return covFReply(s, &Schema{Collection: c}, "GetSchema", c)
}
func (s *covFServer) ListSchemas(_ context.Context) ([]*Schema, error) {
	return covFReply(s, []*Schema{{Collection: "a"}}, "ListSchemas")
}
func (s *covFServer) Me(_ context.Context) (*User, error) {
	return covFReply(s, &User{Username: "me"}, "Me")
}
func (s *covFServer) ListUsers(_ context.Context) ([]*User, error) {
	return covFReply(s, []*User{{Username: "u"}}, "ListUsers")
}
func (s *covFServer) ListGroups(_ context.Context) ([]*Group, error) {
	return covFReply(s, []*Group{{Name: "g"}}, "ListGroups")
}
func (s *covFServer) UserPermissionsList(_ context.Context, u string) ([]*UserPermission, error) {
	return covFReply(s, []*UserPermission{{Username: u}}, "UserPermissionsList", u)
}
func (s *covFServer) GroupPermissionsList(_ context.Context, g string) ([]*GroupPermission, error) {
	return covFReply(s, []*GroupPermission{{GroupName: g}}, "GroupPermissionsList", g)
}

// covFCase drives one resolver and names the server call it must produce.
type covFCase struct {
	name     string
	wantCall string
	run      func(ctx context.Context, m MutationResolver, q QueryResolver) (any, error)
}

func covFCases() []covFCase {
	yes := true
	key := "k1"
	ttl := 60
	coll := "docs"
	meta := []*MetaInput{{Key: "a", Values: []string{"b"}}}
	return []covFCase{
		{"AddDocument", "AddDocument docs", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.AddDocument(ctx, AddDocumentInput{Collection: "docs"})
		}},
		{"UpdateDocument", "UpdateDocument docs", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.UpdateDocument(ctx, UpdateDocumentInput{Collection: "docs"})
		}},
		{"DeleteDocument", "DeleteDocument docs k1 en", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.DeleteDocument(ctx, "docs", "k1", "en")
		}},
		{"DeleteCollection", "DeleteCollection docs", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.DeleteCollection(ctx, "docs")
		}},
		{"AddBatch", "AddBatch docs 2", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.AddBatch(ctx, "docs", []*AddBatchDocumentInput{{Key: "a"}, {Key: "b"}})
		}},
		{"IngestDocuments", "IngestDocuments docs 1 true", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.IngestDocuments(ctx, "docs", []*IngestDocumentInput{{Lang: "en"}}, &IngestOptionsInput{})
		}},
		{"VectorReindex", "VectorReindex docs true", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.VectorReindex(ctx, "docs", &yes)
		}},
		{"SetTTL", "SetTTL docs k1 en 60", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.SetTTL(ctx, "docs", "k1", "en", 60)
		}},
		{"ImportURL", "ImportURL docs https://x.test/a k1 en 1 60", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.ImportURL(ctx, "docs", "https://x.test/a", &key, "en", meta, &ttl)
		}},
		{"RegisterWebhook", "RegisterWebhook https://hook.test", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.RegisterWebhook(ctx, RegisterWebhookInput{URL: "https://hook.test"})
		}},
		{"DeleteWebhook", "DeleteWebhook wh1", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.DeleteWebhook(ctx, "wh1")
		}},
		{"SetSchema", "SetSchema docs", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.SetSchema(ctx, SetSchemaInput{Collection: "docs"})
		}},
		{"DeleteSchema", "DeleteSchema docs", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.DeleteSchema(ctx, "docs")
		}},
		{"ValidateDocument", "ValidateDocument docs 1", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.ValidateDocument(ctx, "docs", meta)
		}},
		{"Register", "Register alice", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.Register(ctx, "alice", "pw")
		}},
		{"CreateAPIKey", "CreateAPIKey ci", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.CreateAPIKey(ctx, CreateAPIKeyInput{Description: "ci"})
		}},
		{"SetPermission", "SetPermission alice", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.SetPermission(ctx, SetPermissionInput{Username: "alice"})
		}},
		{"CreateGroup", "CreateGroup eds", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.CreateGroup(ctx, CreateGroupInput{Name: "eds"})
		}},
		{"UpdateGroup", "UpdateGroup eds desc [a b]", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.UpdateGroup(ctx, "eds", "desc", []string{"a", "b"})
		}},
		{"DeleteGroup", "DeleteGroup eds", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.DeleteGroup(ctx, "eds")
		}},
		{"SetGroupPermission", "SetGroupPermission eds", func(ctx context.Context, m MutationResolver, _ QueryResolver) (any, error) {
			return m.SetGroupPermission(ctx, SetGroupPermissionInput{GroupName: "eds"})
		}},
		{"Search", "SearchDocuments docs", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Search(ctx, SearchInput{Collection: "docs"})
		}},
		{"VectorSearch", "VectorSearch docs", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.VectorSearch(ctx, VectorSearchInput{Collection: "docs"})
		}},
		{"VectorStats", "VectorStats", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.VectorStats(ctx)
		}},
		{"Fts", "FullTextSearch hello", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Fts(ctx, FTSInput{Collection: "docs", Query: "hello"})
		}},
		{"CodeGraph", "CodeGraph main.go", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.CodeGraph(ctx, CodeGraphInput{Collection: "code", Key: "main.go"})
		}},
		{"IndexingStatus", "IndexingStatus docs", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.IndexingStatus(ctx, &coll)
		}},
		{"CodeSymbol", "CodeSymbol code ParseFile", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.CodeSymbol(ctx, CodeSymbolInput{Collection: "code", Name: "ParseFile"})
		}},
		{"Stats", "GetStats", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Stats(ctx)
		}},
		{"Webhooks", "ListWebhooks docs", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Webhooks(ctx, &coll)
		}},
		{"Schema", "GetSchema docs", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Schema(ctx, "docs")
		}},
		{"Schemas", "ListSchemas", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Schemas(ctx)
		}},
		{"Me", "Me", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Me(ctx)
		}},
		{"Users", "ListUsers", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Users(ctx)
		}},
		{"Groups", "ListGroups", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.Groups(ctx)
		}},
		{"UserPermissions", "UserPermissionsList alice", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.UserPermissions(ctx, "alice")
		}},
		{"GroupPermissions", "GroupPermissionsList eds", func(ctx context.Context, _ MutationResolver, q QueryResolver) (any, error) {
			return q.GroupPermissions(ctx, "eds")
		}},
	}
}

// covFResolvers builds the resolvers through the public constructor, the way
// graphql_handler.go wires them.
func covFResolvers(s ServerInterface) (MutationResolver, QueryResolver) {
	r := NewResolver(s)
	return r.Mutation(), r.Query()
}

// Every resolver calls exactly one server method, with the arguments it was
// given, and hands the server's answer back unchanged. Mutations that report
// success as a Boolean report true.
func TestCovFResolversDelegateToTheServer(t *testing.T) {
	for _, tc := range covFCases() {
		t.Run(tc.name, func(t *testing.T) {
			srv := &covFServer{}
			m, q := covFResolvers(srv)
			got, err := tc.run(context.Background(), m, q)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(srv.calls) != 1 || srv.calls[0] != tc.wantCall {
				t.Fatalf("server calls = %q, want [%q]", srv.calls, tc.wantCall)
			}
			want := srv.last
			if want == nil { // error-only server method → Boolean resolver
				want = true
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("resolver returned %#v, want the server's %#v", got, want)
			}
		})
	}
}

// A server failure reaches the client as that error, and a Boolean mutation
// never reports success alongside it.
func TestCovFResolversPropagateServerErrors(t *testing.T) {
	for _, tc := range covFCases() {
		t.Run(tc.name, func(t *testing.T) {
			srv := &covFServer{fail: true}
			m, q := covFResolvers(srv)
			got, err := tc.run(context.Background(), m, q)
			if !errors.Is(err, covFErrBackend) {
				t.Fatalf("err = %v, want the server's error", err)
			}
			if b, ok := got.(bool); ok && b {
				t.Error("a failed mutation reported true")
			}
			if len(srv.calls) != 1 {
				t.Errorf("server calls = %q, want one", srv.calls)
			}
		})
	}
}

// The env argument is a JSON object; only string values are meaningful as
// template variables, so anything else is dropped rather than stringified.
func TestCovFDocumentKeepsOnlyStringEnvValues(t *testing.T) {
	srv := &covFServer{}
	_, q := covFResolvers(srv)
	got, err := q.Document(context.Background(), "docs", "k1", "en", map[string]any{
		"site": "blog", "n": 3, "nested": map[string]any{"x": "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != srv.last {
		t.Errorf("resolver returned %v, want the server's document", got)
	}
	if want := "GetDocument docs k1 en map[site:blog]"; len(srv.calls) != 1 || srv.calls[0] != want {
		t.Errorf("server calls = %q, want [%q]", srv.calls, want)
	}

	srv = &covFServer{}
	_, q = covFResolvers(srv)
	if _, err := q.Document(context.Background(), "docs", "k1", "en", nil); err != nil {
		t.Fatal(err)
	}
	if want := "GetDocument docs k1 en map[]"; srv.calls[0] != want {
		t.Errorf("nil env: call = %q, want %q", srv.calls[0], want)
	}
}

// A token that could not be signed must not produce a payload with an empty
// token: the client would store it and fail on every later request.
func TestCovFLoginFailsWhenTheTokenCannotBeIssued(t *testing.T) {
	stub := &stubServer{
		authenticate: func(string, string) (UserInfo, error) { return UserInfo{Username: "admin", Admin: true}, nil },
		generateJWT: func(u string, admin bool) (string, int64, error) {
			if u != "admin" || !admin {
				t.Errorf("GenerateJWT(%q, %v), want the authenticated user", u, admin)
			}
			return "", 0, covFErrBackend
		},
	}
	m, _ := covFResolvers(stub)
	got, err := m.Login(context.Background(), "admin", "secret")
	if !errors.Is(err, covFErrBackend) || got != nil {
		t.Fatalf("Login = %v, %v; want nil and the signing error", got, err)
	}
}
