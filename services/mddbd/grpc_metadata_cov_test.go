package main

// Coverage for grpc_metadata.go (covE batch): permission, read-only,
// validation, not-found and storage-failure paths of the metadata RPCs.

import (
	"context"
	"strings"
	"testing"
	"time"

	"mddb/internal/storage"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// covEServer returns a gRPC server with every manager wired, cleaned up with
// the test.
func covEServer(t *testing.T) (*GRPCServer, *Server) {
	t.Helper()
	gs, s, cleanup := newTestGRPCServerFull(t)
	t.Cleanup(cleanup)
	return gs, s
}

// covEDenyAll switches authentication on. A context without claims is then
// refused by every permission check.
func covEDenyAll(t *testing.T, s *Server) {
	t.Helper()
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	am, cleanup := authMwSetup(t)
	t.Cleanup(cleanup)
	s.AuthManager = am
}

// covEClaimsCtx is a context carrying the given claims.
func covEClaimsCtx(claims *JWTClaims) context.Context {
	return context.WithValue(context.Background(), authContextKey, claims)
}

// covEAdminCtx is the context of a global administrator.
func covEAdminCtx() context.Context {
	return covEClaimsCtx(&JWTClaims{Username: "admin", Admin: true})
}

// covEWant fails the test unless err carries the wanted code and, when
// given, a message containing part.
func covEWant(t *testing.T, name string, err error, want codes.Code, part string) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("%s: code %v, want %v (err %v)", name, got, want, err)
	}
	if part != "" && !strings.Contains(status.Convert(err).Message(), part) {
		t.Fatalf("%s: message %q lacks %q", name, status.Convert(err).Message(), part)
	}
}

// covECloseDB closes the database so every storage call fails.
func covECloseDB(t *testing.T, s *Server) {
	t.Helper()
	if err := s.DB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// covERPC is one RPC call reduced to its error.
type covERPC struct {
	name string
	call func(ctx context.Context) error
}

// covERun calls each RPC and requires the wanted code and message part.
func covERun(t *testing.T, ctx context.Context, rpcs []covERPC, want codes.Code, part string) {
	t.Helper()
	for _, r := range rpcs {
		covEWant(t, r.name, r.call(ctx), want, part)
	}
}

func covEMetadataGuarded(gs *GRPCServer) []covERPC {
	return []covERPC{
		{"RegisterWebhook", func(ctx context.Context) error {
			_, err := gs.RegisterWebhook(ctx, &pb.RegisterWebhookRequest{Collection: "c", Url: "https://example.com"})
			return err
		}},
		{"ListWebhooks", func(ctx context.Context) error {
			_, err := gs.ListWebhooks(ctx, &pb.ListWebhooksRequest{})
			return err
		}},
		{"DeleteWebhook", func(ctx context.Context) error {
			_, err := gs.DeleteWebhook(ctx, &pb.DeleteWebhookRequest{Id: "x"})
			return err
		}},
		{"SetSchema", func(ctx context.Context) error {
			_, err := gs.SetSchema(ctx, &pb.SetSchemaRequest{Collection: "c", Schema: "{}"})
			return err
		}},
		{"GetSchema", func(ctx context.Context) error {
			_, err := gs.GetSchema(ctx, &pb.GetSchemaRequest{Collection: "c"})
			return err
		}},
		{"DeleteSchema", func(ctx context.Context) error {
			_, err := gs.DeleteSchema(ctx, &pb.DeleteSchemaRequest{Collection: "c"})
			return err
		}},
		{"ListSchemas", func(ctx context.Context) error {
			_, err := gs.ListSchemas(ctx, &pb.ListSchemasRequest{})
			return err
		}},
		{"ValidateDocument", func(ctx context.Context) error {
			_, err := gs.ValidateDocument(ctx, &pb.ValidateDocumentRequest{Collection: "c"})
			return err
		}},
		{"UpdateDocument", func(ctx context.Context) error {
			_, err := gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "c", Key: "k", Lang: "en", UpdateContent: true})
			return err
		}},
		{"GetDocumentMeta", func(ctx context.Context) error {
			_, err := gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"Classify", func(ctx context.Context) error {
			_, err := gs.Classify(ctx, &pb.ClassifyRequest{Collection: "c", Key: "k", Labels: []string{"a"}})
			return err
		}},
		{"DeleteDocument", func(ctx context.Context) error {
			_, err := gs.DeleteDocument(ctx, &pb.DeleteDocumentRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"DeleteCollection", func(ctx context.Context) error {
			_, err := gs.DeleteCollection(ctx, &pb.DeleteCollectionRequest{Collection: "c"})
			return err
		}},
		{"GetMetaKeys", func(ctx context.Context) error {
			_, err := gs.GetMetaKeys(ctx, &pb.GetMetaKeysRequest{Collection: "c"})
			return err
		}},
		{"GetChecksum", func(ctx context.Context) error {
			_, err := gs.GetChecksum(ctx, &pb.GetChecksumRequest{Collection: "c"})
			return err
		}},
	}
}

func TestCovEMetadataPermissionDenied(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	covERun(t, context.Background(), covEMetadataGuarded(gs), codes.PermissionDenied, "")

	// Nothing was written behind the refusals.
	if _, found := s.SchemaManager.Get("c"); found {
		t.Fatal("refused SetSchema stored a schema")
	}
	if n := len(s.WebhookManager.List()); n != 0 {
		t.Fatalf("refused RegisterWebhook stored %d hooks", n)
	}
}

func TestCovEMetadataReadOnly(t *testing.T) {
	gs, s := covEServer(t)
	s.Mode = ModeRead
	ctx := context.Background()
	writes := []covERPC{
		{"AddSynonym", func(ctx context.Context) error {
			_, err := gs.AddSynonym(ctx, &pb.AddSynonymRequest{Collection: "c", Term: "t", Synonyms: []string{"s"}})
			return err
		}},
		{"DeleteSynonym", func(ctx context.Context) error {
			_, err := gs.DeleteSynonym(ctx, &pb.DeleteSynonymRequest{Collection: "c", Term: "t"})
			return err
		}},
		{"AddStopwords", func(ctx context.Context) error {
			_, err := gs.AddStopwords(ctx, &pb.AddStopwordsRequest{Collection: "c", Words: []string{"w"}})
			return err
		}},
		{"DeleteStopwords", func(ctx context.Context) error {
			_, err := gs.DeleteStopwords(ctx, &pb.DeleteStopwordsRequest{Collection: "c", Words: []string{"w"}})
			return err
		}},
	}
	for _, r := range covEMetadataGuarded(gs) {
		switch r.name {
		case "RegisterWebhook", "DeleteWebhook", "SetSchema", "DeleteSchema", "UpdateDocument", "DeleteDocument", "DeleteCollection":
			writes = append(writes, r)
		}
	}
	covERun(t, ctx, writes, codes.PermissionDenied, "read-only mode")

	// Reads still answer in read-only mode.
	if _, err := gs.GetSchema(ctx, &pb.GetSchemaRequest{Collection: "c"}); err != nil {
		t.Fatalf("read in read-only mode: %v", err)
	}
}

func TestCovEMetadataStorageFailures(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	covECloseDB(t, s)

	covERun(t, ctx, []covERPC{
		{"DeleteWebhook", func(ctx context.Context) error {
			_, err := gs.DeleteWebhook(ctx, &pb.DeleteWebhookRequest{Id: "x"})
			return err
		}},
		{"DeleteSchema", func(ctx context.Context) error {
			_, err := gs.DeleteSchema(ctx, &pb.DeleteSchemaRequest{Collection: "c"})
			return err
		}},
		{"GetDocumentMeta", func(ctx context.Context) error {
			_, err := gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"DeleteDocument", func(ctx context.Context) error {
			_, err := gs.DeleteDocument(ctx, &pb.DeleteDocumentRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"DeleteCollection", func(ctx context.Context) error {
			_, err := gs.DeleteCollection(ctx, &pb.DeleteCollectionRequest{Collection: "c"})
			return err
		}},
		{"DeleteSynonym", func(ctx context.Context) error {
			_, err := gs.DeleteSynonym(ctx, &pb.DeleteSynonymRequest{Collection: "c", Term: "t"})
			return err
		}},
	}, codes.Internal, "database not open")
}

func TestCovEListSchemasTenantScope(t *testing.T) {
	gs, s := covEServer(t)
	for _, c := range []string{"acme/docs", "other"} {
		if err := s.SchemaManager.Set(c, `{"properties":{}}`); err != nil {
			t.Fatalf("set schema %s: %v", c, err)
		}
	}
	resp, err := gs.ListSchemas(covEClaimsCtx(&JWTClaims{Username: "u", Tenant: "acme"}), &pb.ListSchemasRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Schemas) != 1 || resp.Schemas[0].Collection != "acme/docs" {
		t.Fatalf("tenant saw %+v, want only acme/docs", resp.Schemas)
	}
	all, err := gs.ListSchemas(context.Background(), &pb.ListSchemasRequest{})
	if err != nil || len(all.Schemas) != 2 {
		t.Fatalf("global listing: %v %+v", err, all)
	}
}

func TestCovEUpdateDocumentPaths(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "c", "k", "en", "body", map[string][]string{"status": {"draft"}})

	_, err := gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "c", Key: "k"})
	covEWant(t, "missing lang", err, codes.InvalidArgument, "missing required fields")
	_, err = gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "c", Key: "k", Lang: "en"})
	covEWant(t, "nothing to update", err, codes.InvalidArgument, "no fields to update")

	before := time.Now().Unix()
	doc, err := gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "c", Key: "k", Lang: "en", UpdateTtl: true, Ttl: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ExpiresAt < before+3600 || doc.ContentMd != "body" || doc.Revision == "" {
		t.Fatalf("ttl update: %+v", doc)
	}

	if err := s.SchemaManager.Set("c", `{"required":["title"]}`); err != nil {
		t.Fatal(err)
	}
	_, err = gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "c", Key: "k", Lang: "en", UpdateMeta: true,
		Meta: map[string]*pb.MetaValues{"status": {Values: []string{"live"}}}})
	covEWant(t, "schema violation", err, codes.InvalidArgument, "title")
}

// covEPutRaw writes raw bykey and docs entries for a document.
func covEPutRaw(t *testing.T, s *Server, coll, key, lang string, docBytes []byte) {
	t.Helper()
	id := genID(coll, key, lang)
	if err := s.DBUpdateC(coll, func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte("bykey")).Put(storage.ByKeyKey(coll, key, lang), []byte(id)); err != nil {
			return err
		}
		if docBytes == nil {
			return nil
		}
		return tx.Bucket([]byte("docs")).Put(storage.DocKey(coll, id), docBytes)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCovEGetDocumentMeta(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()

	_, err := gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "k"})
	covEWant(t, "missing lang", err, codes.InvalidArgument, "missing required fields")
	_, err = gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "nope", Lang: "en"})
	covEWant(t, "no key", err, codes.NotFound, "document not found")

	// An index entry whose document is gone.
	covEPutRaw(t, s, "c", "dangling", "en", nil)
	_, err = gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "dangling", Lang: "en"})
	covEWant(t, "dangling", err, codes.NotFound, "document not found")

	// A document that does not decode.
	covEPutRaw(t, s, "c", "corrupt", "en", []byte{0xff, 0xfe, 0xfd})
	_, err = gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "corrupt", Lang: "en"})
	covEWant(t, "corrupt", err, codes.Internal, "")

	// An expired document is not served.
	expired := storage.Doc{ID: genID("c", "old", "en"), Key: "old", Lang: "en", ContentMD: "x", ExpiresAt: time.Now().Unix() - 60}
	buf, err := marshalDoc(&expired)
	if err != nil {
		t.Fatal(err)
	}
	covEPutRaw(t, s, "c", "old", "en", buf)
	_, err = gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "old", Lang: "en"})
	covEWant(t, "expired", err, codes.NotFound, "document not found")

	added := addTestDoc(t, s, "c", "live", "en", "secret body", map[string][]string{"tag": {"a", "b"}})
	resp, err := gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "live", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Key != "live" || resp.Lang != "en" || resp.AddedAt != added.AddedAt ||
		strings.Join(resp.Meta["tag"].Values, ",") != "a,b" {
		t.Fatalf("meta: %+v", resp)
	}
}

func TestCovEClassify(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()

	_, err := gs.Classify(ctx, &pb.ClassifyRequest{Text: "x"})
	covEWant(t, "no labels", err, codes.InvalidArgument, "labels are required")
	_, err = gs.Classify(ctx, &pb.ClassifyRequest{Text: "x", Labels: []string{"a"}})
	covEWant(t, "no provider", err, codes.FailedPrecondition, "no embedding provider")

	s.Embedding = &mockEmbedding{dims: 3, model: "covE-model"}
	_, err = gs.Classify(ctx, &pb.ClassifyRequest{Collection: "c", Key: "missing", Labels: []string{"a"}})
	covEWant(t, "missing doc", err, codes.NotFound, "document not found")
	_, err = gs.Classify(ctx, &pb.ClassifyRequest{Text: "x", Labels: make([]string, 101)})
	covEWant(t, "too many labels", err, codes.InvalidArgument, "maximum 100 labels")

	resp, err := gs.Classify(ctx, &pb.ClassifyRequest{Text: "invoice", Labels: []string{"billing", "sports"}, TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "covE-model" || resp.Dimensions != 3 || len(resp.Results) != 2 || resp.Results[0].Label == "" {
		t.Fatalf("classify: %+v", resp)
	}
}

func TestCovEDeleteDocumentAndCollection(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "c", "k", "en", "body", nil)
	addTestDoc(t, s, "c", "k2", "en", "body", nil)

	_, err := gs.DeleteDocument(ctx, &pb.DeleteDocumentRequest{Collection: "c", Key: "missing", Lang: "en"})
	covEWant(t, "missing doc", err, codes.NotFound, "document not found")

	del, err := gs.DeleteDocument(ctx, &pb.DeleteDocumentRequest{Collection: "c", Key: "k", Lang: "en"})
	if err != nil || del.Status != "deleted" || del.Key != "k" {
		t.Fatalf("delete: %v %+v", err, del)
	}
	_, err = gs.GetDocumentMeta(ctx, &pb.GetDocumentMetaRequest{Collection: "c", Key: "k", Lang: "en"})
	covEWant(t, "deleted doc", err, codes.NotFound, "")

	_, err = gs.DeleteCollection(ctx, &pb.DeleteCollectionRequest{})
	covEWant(t, "no collection", err, codes.InvalidArgument, "missing collection")
	dc, err := gs.DeleteCollection(ctx, &pb.DeleteCollectionRequest{Collection: "c"})
	if err != nil || dc.DeletedCount != 1 {
		t.Fatalf("delete collection: %v %+v", err, dc)
	}
}

func TestCovESynonymsAndStopwordsWithoutManagers(t *testing.T) {
	gs, s := covEServer(t)
	s.SynonymManager, s.StopWordManager = nil, nil
	ctx := context.Background()
	covERun(t, ctx, []covERPC{
		{"ListSynonyms", func(ctx context.Context) error {
			_, err := gs.ListSynonyms(ctx, &pb.ListSynonymsRequest{Collection: "c"})
			return err
		}},
		{"AddSynonym", func(ctx context.Context) error {
			_, err := gs.AddSynonym(ctx, &pb.AddSynonymRequest{Collection: "c", Term: "t", Synonyms: []string{"s"}})
			return err
		}},
		{"DeleteSynonym", func(ctx context.Context) error {
			_, err := gs.DeleteSynonym(ctx, &pb.DeleteSynonymRequest{Collection: "c", Term: "t"})
			return err
		}},
		{"ListStopwords", func(ctx context.Context) error {
			_, err := gs.ListStopwords(ctx, &pb.ListStopwordsRequest{Collection: "c"})
			return err
		}},
		{"AddStopwords", func(ctx context.Context) error {
			_, err := gs.AddStopwords(ctx, &pb.AddStopwordsRequest{Collection: "c", Words: []string{"w"}})
			return err
		}},
		{"DeleteStopwords", func(ctx context.Context) error {
			_, err := gs.DeleteStopwords(ctx, &pb.DeleteStopwordsRequest{Collection: "c", Words: []string{"w"}})
			return err
		}},
	}, codes.FailedPrecondition, "not initialized")
}

func TestCovESynonymsAndStopwords(t *testing.T) {
	gs, _ := covEServer(t)
	ctx := context.Background()

	_, err := gs.AddSynonym(ctx, &pb.AddSynonymRequest{Collection: "c", Term: "   ", Synonyms: []string{"s"}})
	covEWant(t, "blank term", err, codes.InvalidArgument, "missing required fields")
	_, err = gs.DeleteSynonym(ctx, &pb.DeleteSynonymRequest{Collection: "c"})
	covEWant(t, "delete without term", err, codes.InvalidArgument, "missing required fields")

	_, err = gs.AddStopwords(ctx, &pb.AddStopwordsRequest{Collection: "c", Words: []string{"  "}})
	covEWant(t, "blank words", err, codes.InvalidArgument, "missing required fields")
	_, err = gs.DeleteStopwords(ctx, &pb.DeleteStopwordsRequest{Collection: "c"})
	covEWant(t, "delete without words", err, codes.InvalidArgument, "missing required fields")

	if _, err := gs.AddStopwords(ctx, &pb.AddStopwordsRequest{Collection: "c", Words: []string{"lorem"}}); err != nil {
		t.Fatal(err)
	}
	// A default stop word cannot be deleted; the custom one can.
	resp, err := gs.DeleteStopwords(ctx, &pb.DeleteStopwordsRequest{Collection: "c", Words: []string{"the", "lorem"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Deleted != 1 || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0], "default stop word") {
		t.Fatalf("delete stopwords: %+v", resp)
	}
	list, err := gs.ListStopwords(ctx, &pb.ListStopwordsRequest{Collection: "c"})
	if err != nil || list.Custom != 0 {
		t.Fatalf("custom stopword survived: %v %+v", err, list)
	}
}

func TestCovEGetMetaKeys(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "c", "k", "en", "body", map[string][]string{"tag": {"x"}})
	// A malformed index key (no value part) is skipped, not reported.
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("idxmeta")).Put([]byte("meta|c|broken"), []byte{})
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := gs.GetMetaKeys(ctx, &pb.GetMetaKeysRequest{Collection: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Meta) != 1 || resp.Meta["tag"].Values[0] != "x" {
		t.Fatalf("meta keys: %+v", resp.Meta)
	}

	// Without the index bucket there is simply nothing to report.
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("idxmeta")) }); err != nil {
		t.Fatal(err)
	}
	resp, err = gs.GetMetaKeys(ctx, &pb.GetMetaKeysRequest{Collection: "c"})
	if err != nil || len(resp.Meta) != 0 {
		t.Fatalf("no bucket: %v %+v", err, resp)
	}
}

func TestCovEMetadataAllowedForAdmin(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	ctx := covEAdminCtx()
	addTestDoc(t, s, "c", "k", "en", "body", nil)
	sum, err := gs.GetChecksum(ctx, &pb.GetChecksumRequest{Collection: "c"})
	if err != nil || sum.DocumentCount != 1 || sum.Checksum == "" {
		t.Fatalf("checksum as admin: %v %+v", err, sum)
	}
	wh, err := gs.RegisterWebhook(ctx, &pb.RegisterWebhookRequest{Collection: "c", Url: "https://example.com/h", Events: []string{"doc.added"}})
	if err != nil || wh.Id == "" {
		t.Fatalf("register as admin: %v %+v", err, wh)
	}
	list, err := gs.ListWebhooks(ctx, &pb.ListWebhooksRequest{})
	if err != nil || len(list.Webhooks) != 1 || list.Webhooks[0].Url != "https://example.com/h" {
		t.Fatalf("list as admin: %v %+v", err, list)
	}
}

func TestCovEStopwordsMissingFields(t *testing.T) {
	gs, _ := covEServer(t)
	ctx := context.Background()
	_, err := gs.ListStopwords(ctx, &pb.ListStopwordsRequest{})
	covEWant(t, "list", err, codes.InvalidArgument, "missing collection")
	_, err = gs.AddStopwords(ctx, &pb.AddStopwordsRequest{Collection: "c"})
	covEWant(t, "add", err, codes.InvalidArgument, "missing required fields")
}
