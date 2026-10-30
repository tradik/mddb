package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/embedding"
	json "mddb/internal/jsonx"
	"mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// #378: corpus packs over MCP and gRPC, through the functions REST uses.

// packServer serves pack over TLS at /docs.mddbpack and makes it the server
// corpus packs are fetched from: corpusFetchClient trusts its certificate.
func packServer(t *testing.T, pack []byte) *httptest.Server {
	t.Helper()
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs.mddbpack":
			_, _ = w.Write(pack)
		case "/plain":
			http.Redirect(w, r, "http://example.com/docs.mddbpack", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	prev := corpusFetchClient
	corpusFetchClient = func() *http.Client {
		c := srv.Client()
		c.CheckRedirect = httpsOnlyRedirect(func(*http.Request, []*http.Request) error { return nil })
		return c
	}
	t.Cleanup(func() { corpusFetchClient = prev })
	return srv
}

// corpusPackFixture is a server with the "docs" pack built on another one.
func corpusPackFixture(t *testing.T) (*Server, []byte) {
	t.Helper()
	t.Setenv("MDDB_EMBEDDING_PROVIDER", embedding.ProviderLexical)
	t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "256")
	src := corpusServer(t, 256)
	seedCorpus(t, src)
	return corpusServer(t, 256), buildPack(t, src, "docs")
}

func sum256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestCorpusPackOverMCP(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			dst, pack := corpusPackFixture(t)
			srv := packServer(t, pack)
			ts := &MCPToolServer{client: NewDirectClient(dst), globalMode: ModeRW}
			installOverMCP(t, ts, srv.URL+"/docs.mddbpack", sum256(pack))
			removeOverMCP(t, ts)
		})
	}
}

// installOverMCP installs and lists the "docs" pack with the MCP tools.
func installOverMCP(t *testing.T, ts *MCPToolServer, url, sum string) {
	t.Helper()
	ctx := context.Background()
	if _, err := ts.mcpCallTool(ctx, "corpus_install", map[string]interface{}{}); err == nil {
		t.Error("an install without a url was accepted")
	}
	out, err := ts.mcpCallTool(ctx, "corpus_install", map[string]interface{}{"url": url, "sha256": strings.ToUpper(sum)})
	if err != nil || !strings.Contains(out, `"docCount": 3`) {
		t.Fatalf("corpus_install = %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(ctx, "corpus_list", nil)
	if err != nil || !strings.Contains(out, `"total": 1`) || !strings.Contains(out, `"collection": "docs"`) {
		t.Fatalf("corpus_list = %s, %v", out, err)
	}
}

// removeOverMCP removes the "docs" pack with the MCP tool, then again.
func removeOverMCP(t *testing.T, ts *MCPToolServer) {
	t.Helper()
	ctx := context.Background()
	if _, err := ts.mcpCallTool(ctx, "corpus_remove", map[string]interface{}{}); err == nil {
		t.Error("a remove without a name was accepted")
	}
	out, err := ts.mcpCallTool(ctx, "corpus_remove", map[string]interface{}{"name": "docs"})
	if err != nil || out != `{"status":"removed","name":"docs","deleted":3}` {
		t.Fatalf("corpus_remove = %s, %v", out, err)
	}
	if _, err := ts.mcpCallTool(ctx, "corpus_remove", map[string]interface{}{"name": "docs"}); !errors.Is(err, errNotFound) {
		t.Errorf("removing it twice = %v", err)
	}
}

func TestCorpusInstallFromURLRefuses(t *testing.T) {
	dst, pack := corpusPackFixture(t)
	srv := packServer(t, pack)
	ctx := context.Background()
	for name, tc := range map[string]struct{ url, sum, want string }{
		"http":           {"http://example.com/p.mddbpack", "", "https only"},
		"no host":        {"https:///p", "", "https only"},
		"not a URL":      {"https://%zz", "", "https only"},
		"404":            {srv.URL + "/missing", "", "404"},
		"redirect off":   {srv.URL + "/plain", "", "https only"},
		"wrong checksum": {srv.URL + "/docs.mddbpack", strings.Repeat("0", 64), "checksum mismatch"},
	} {
		_, err := dst.installCorpusFromURL(ctx, tc.url, tc.sum)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	if dst.collectionHasData("docs") {
		t.Fatal("a refused install left data")
	}
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false")
	if _, err := dst.installCorpusFromURL(ctx, srv.URL+"/docs.mddbpack", ""); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("the SSRF guard let a loopback URL through: %v", err)
	}
}

func TestCorpusInstallCapped(t *testing.T) {
	dst, pack := corpusPackFixture(t)
	t.Setenv("MDDB_CORPUS_MAX_BYTES", "64")
	for _, sum := range []string{"", sum256(pack)} {
		if _, err := dst.installCorpusChecked(bytes.NewReader(pack), sum); !errors.Is(err, errCorpusInvalid) {
			t.Errorf("sha256 %q: a pack past the cap = %v", sum, err)
		}
	}
	// The cap itself: exactly at it reads, one byte past it fails, and a
	// read after the failure fails again.
	r := &corpusCapReader{r: strings.NewReader("abcd"), left: 4}
	if b, err := io.ReadAll(r); err != nil || string(b) != "abcd" {
		t.Errorf("at the cap: %q, %v", b, err)
	}
	r = &corpusCapReader{r: strings.NewReader("abcde"), left: 4}
	if _, err := io.ReadAll(r); !errors.Is(err, errCorpusTooLarge) {
		t.Errorf("past the cap: %v", err)
	}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, errCorpusTooLarge) {
		t.Errorf("read after the cap: %v", err)
	}
}

func TestCorpusInstallCheckedStagingFails(t *testing.T) {
	s := &Server{Path: "/nonexistent-dir/x/mddb.db"}
	if _, err := s.installCorpusChecked(strings.NewReader("x"), "ab"); err == nil {
		t.Error("staging in a missing directory succeeded")
	}
	dst, _ := corpusPackFixture(t)
	if _, err := dst.installCorpusChecked(iotestErrReader{}, "ab"); err == nil {
		t.Error("a failing reader was installed")
	}
}

// iotestErrReader fails every read.
type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestDefaultCorpusFetchClientRefusesPlainRedirects(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "false") // TestMain allows it for the whole binary
	c := corpusFetchClient()
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err := c.CheckRedirect(req, nil); err == nil {
		t.Error("a redirect to http was followed")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://127.0.0.1/", nil)
	if err := c.CheckRedirect(req, nil); err == nil {
		t.Error("a redirect to loopback passed the SSRF check")
	}
}

// --- gRPC ---

// installStream is a client stream of install messages.
type installStream struct {
	grpc.ServerStream
	ctx  context.Context
	msgs []*proto.InstallCorpusPackRequest
	resp *proto.InstallCorpusPackResponse
}

func (s *installStream) Context() context.Context { return s.ctx }

func (s *installStream) Recv() (*proto.InstallCorpusPackRequest, error) {
	if len(s.msgs) == 0 {
		return nil, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	return m, nil
}

func (s *installStream) SendAndClose(r *proto.InstallCorpusPackResponse) error {
	s.resp = r
	return nil
}

// chunked splits pack into install messages of n bytes, sum on the first.
func chunked(pack []byte, n int, sum string) []*proto.InstallCorpusPackRequest {
	var out []*proto.InstallCorpusPackRequest
	for len(pack) > 0 {
		k := min(n, len(pack))
		out = append(out, &proto.InstallCorpusPackRequest{Data: pack[:k]})
		pack = pack[k:]
	}
	out[0].Sha256 = sum
	return out
}

// buildStream collects a BuildCorpusPack stream.
type buildStream struct {
	grpc.ServerStream
	ctx    context.Context
	chunks []*proto.CorpusPackChunk
	fail   bool
}

func (s *buildStream) Context() context.Context { return s.ctx }

func (s *buildStream) Send(c *proto.CorpusPackChunk) error {
	if s.fail {
		return errors.New("client gone")
	}
	s.chunks = append(s.chunks, c)
	return nil
}

func grpcCode(err error) codes.Code { return status.Code(err) }

func TestCorpusPackOverGRPC(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			t.Setenv("MDDB_EMBEDDING_PROVIDER", embedding.ProviderLexical)
			t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "256")
			src := corpusServer(t, 256)
			seedCorpus(t, src)
			pack := buildOverGRPC(t, NewGRPCServer(src))

			g := NewGRPCServer(corpusServer(t, 256))
			installOverGRPC(t, g, pack)
			removeOverGRPC(t, g)

			// From a URL, as MCP installs it.
			srv := packServer(t, pack)
			st := &installStream{ctx: context.Background(), msgs: []*proto.InstallCorpusPackRequest{{Url: srv.URL + "/docs.mddbpack"}}}
			if err := g.InstallCorpusPack(st); err != nil || st.resp.Name != "docs" {
				t.Fatalf("install from a URL = %+v, %v", st.resp, err)
			}
		})
	}
}

// buildOverGRPC builds the "docs" pack in chunks; the first one names the file.
func buildOverGRPC(t *testing.T, g *GRPCServer) []byte {
	t.Helper()
	bs := &buildStream{ctx: context.Background()}
	if err := g.BuildCorpusPack(&proto.BuildCorpusPackRequest{Collection: "docs", Version: "1.0", License: "CC-BY-4.0",
		Sources: []*proto.CorpusSourceProto{{Repo: "https://example.org/docs", License: "CC-BY-4.0"}}}, bs); err != nil {
		t.Fatal(err)
	}
	if len(bs.chunks) == 0 || bs.chunks[0].Filename != "docs-1.0.mddbpack" || bs.chunks[0].DocCount != 3 {
		t.Fatalf("first chunk = %+v", bs.chunks)
	}
	var pack []byte
	for _, c := range bs.chunks {
		pack = append(pack, c.Data...)
	}
	return pack
}

// installOverGRPC streams the pack in, twice, and lists it.
func installOverGRPC(t *testing.T, g *GRPCServer, pack []byte) {
	t.Helper()
	ctx := context.Background()
	st := &installStream{ctx: ctx, msgs: chunked(pack, 4096, sum256(pack))}
	if err := g.InstallCorpusPack(st); err != nil || st.resp.Name != "docs" || st.resp.DocCount != 3 {
		t.Fatalf("install = %+v, %v", st.resp, err)
	}
	if err := g.InstallCorpusPack(&installStream{ctx: ctx, msgs: chunked(pack, 1<<20, "")}); grpcCode(err) != codes.AlreadyExists {
		t.Errorf("installed twice: %v", err)
	}
	list, err := g.ListCorpusPacks(ctx, &proto.ListCorpusPacksRequest{})
	if err != nil || list.Total != 1 || list.Packs[0].Collection != "docs" || !list.Packs[0].ReadOnly || list.Packs[0].Corpus.DocCount != 3 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

// removeOverGRPC removes the pack, then again.
func removeOverGRPC(t *testing.T, g *GRPCServer) {
	t.Helper()
	ctx := context.Background()
	rm, err := g.RemoveCorpusPack(ctx, &proto.RemoveCorpusPackRequest{Name: "docs"})
	if err != nil || rm.Deleted != 3 || rm.Status != "removed" {
		t.Fatalf("remove = %+v, %v", rm, err)
	}
	if _, err := g.RemoveCorpusPack(ctx, &proto.RemoveCorpusPackRequest{Name: "docs"}); grpcCode(err) != codes.NotFound {
		t.Errorf("removed twice: %v", err)
	}
}

func TestCorpusGRPCRefuses(t *testing.T) {
	dst, pack := corpusPackFixture(t)
	g := NewGRPCServer(dst)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		msgs []*proto.InstallCorpusPackRequest
		code codes.Code
	}{
		"nothing sent":      {nil, codes.InvalidArgument},
		"url and data":      {[]*proto.InstallCorpusPackRequest{{Url: "https://x/p", Data: []byte("x")}}, codes.InvalidArgument},
		"http url":          {[]*proto.InstallCorpusPackRequest{{Url: "http://x/p"}}, codes.InvalidArgument},
		"url after data":    {[]*proto.InstallCorpusPackRequest{{Data: pack[:10]}, {Url: "https://x/p"}}, codes.InvalidArgument},
		"not a pack":        {[]*proto.InstallCorpusPackRequest{{Data: []byte("not a pack")}}, codes.InvalidArgument},
		"checksum mismatch": {chunked(pack, 1<<20, "00"), codes.InvalidArgument},
	} {
		if err := g.InstallCorpusPack(&installStream{ctx: ctx, msgs: tc.msgs}); grpcCode(err) != tc.code {
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
	}
	if err := g.BuildCorpusPack(&proto.BuildCorpusPackRequest{Collection: "none", License: "MIT"}, &buildStream{ctx: ctx}); grpcCode(err) != codes.NotFound {
		t.Errorf("build of a missing collection: %v", err)
	}
	if err := g.BuildCorpusPack(&proto.BuildCorpusPackRequest{Collection: "docs"}, &buildStream{ctx: ctx}); grpcCode(err) != codes.InvalidArgument {
		t.Errorf("build without a licence: %v", err)
	}
	if got := grpcCode(corpusGRPCError(errors.New("disk full"))); got != codes.Internal {
		t.Errorf("a server fault maps to %s", got)
	}
}

func TestCorpusGRPCBuildToAClientThatLeft(t *testing.T) {
	src := corpusServer(t, 256)
	seedCorpus(t, src)
	err := NewGRPCServer(src).BuildCorpusPack(&proto.BuildCorpusPackRequest{Collection: "docs", License: "MIT"}, &buildStream{ctx: context.Background(), fail: true})
	if grpcCode(err) != codes.Internal {
		t.Errorf("a gone client: %v", err)
	}
}

func TestCorpusGRPCNeedsAdminAndWrites(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	g := NewGRPCServer(s)
	user := authenticatedContext("someone", false)
	if _, err := g.RemoveCorpusPack(user, &proto.RemoveCorpusPackRequest{Name: "x"}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("remove by a non-admin: %v", err)
	}
	if err := g.InstallCorpusPack(&installStream{ctx: user}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("install by a non-admin: %v", err)
	}
	if err := g.BuildCorpusPack(&proto.BuildCorpusPackRequest{Collection: "x"}, &buildStream{ctx: user}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("build by a non-admin: %v", err)
	}
	s.Mode = ModeRead
	if _, err := g.RemoveCorpusPack(context.Background(), &proto.RemoveCorpusPackRequest{Name: "x"}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("remove on a read-only server: %v", err)
	}
}

func TestCorpusListJSONShape(t *testing.T) {
	// The MCP answer is the REST answer: {"packs": [...], "total": n}.
	ts := &MCPToolServer{client: NewDirectClient(&Server{}), globalMode: ModeRW}
	out, err := ts.toolCorpusList(context.Background(), nil)
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got["total"] != float64(0) {
		t.Errorf("empty list = %s, %v", out, err)
	}
}

func TestCorpusStreamReader(t *testing.T) {
	r := &corpusStreamReader{stream: &installStream{ctx: context.Background(), msgs: []*proto.InstallCorpusPackRequest{
		{Data: []byte("ab")}, {}, {Data: []byte("c")}, {Url: "https://x/p"},
	}}}
	b, err := io.ReadAll(r)
	if string(b) != "abc" || !errors.Is(err, errCorpusInvalid) {
		t.Errorf("read %q, %v; want abc and a refused url", b, err)
	}
	r = &corpusStreamReader{stream: &installStream{ctx: context.Background()}, buf: []byte("x")}
	if b, err := io.ReadAll(r); string(b) != "x" || err != nil {
		t.Errorf("read %q, %v; want x to the end of the stream", b, err)
	}
}

func TestCorpusGRPCAdminPasses(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	admin := authenticatedContext("root", true)
	if _, err := NewGRPCServer(s).RemoveCorpusPack(admin, &proto.RemoveCorpusPackRequest{Name: "none"}); grpcCode(err) != codes.NotFound {
		t.Errorf("remove by an admin: %v", err)
	}
}

// A sender without a progress token, or none at all, sends nothing.
func TestCorpusInstallProgressSenderNil(t *testing.T) {
	(*MCPProgressSender)(nil).Send(1, 2, "corpus_install")
	NewCallbackProgressSender(nil, func(map[string]interface{}) { t.Error("sent without a token") }).Send(1, 2, "")
}
func (c *covBAllErrClient) CorpusList(context.Context) ([]corpusListEntry, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) CorpusInstall(context.Context, string, string) (*corpusInstallResult, error) {
	return nil, errCovBClient
}
func (c *covBAllErrClient) CorpusRemove(context.Context, string) (int, error) {
	return 0, errCovBClient
}

// TestCovBCorpusToolsPassClientErrors: the corpus tools (#378) return the
// client's failure unchanged once their arguments are there.
func TestCovBCorpusToolsPassClientErrors(t *testing.T) {
	ts := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	for name, args := range map[string]map[string]interface{}{
		"corpus_list": nil, "corpus_install": {"url": "https://x/p"}, "corpus_remove": {"name": "p"},
	} {
		if _, err := ts.mcpCallTool(context.Background(), name, args); !errors.Is(err, errCovBClient) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
