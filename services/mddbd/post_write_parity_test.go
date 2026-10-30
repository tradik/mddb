package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	"mddb/internal/binlog"
	"mddb/internal/geo"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// GO-048: every write path ends in the same after-write pipeline
// (firePostBatchHooks). These tests send the same update through each path
// and expect the same effects on the full-text index, the embedding queue,
// geo, webhooks, SSE and the binlog. UpdateBatch used to re-index FTS and
// nothing else, and a document whose content was emptied kept its old
// postings and vectors on every path.

const parityKey = "parity-doc"

// writeProbe is a server with every after-write consumer wired to something
// a test can read.
type writeProbe struct {
	s        *Server
	worker   *EmbeddingWorker // never started: jobs stay queued until drained
	webhooks chan string      // "event key" per delivered webhook
	sse      *sseClient
	docID    string
}

func newWriteProbe(t *testing.T) *writeProbe {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	p := &writeProbe{s: s, webhooks: make(chan string, 64), docID: genID("c", parityKey, "en")}

	p.worker = NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, s.VectorStore, s.VectorIndex, 64)
	p.worker.SetIndexer(serverIndexes{s})
	s.EmbeddingWorker = p.worker

	s.GeoIndex, s.GeoHashIndex, s.GeoStore = geo.NewGeoIndex(), geo.NewGeoHashIndex(), geo.NewGeoStore(s.DB)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct{ Event, Key string }
		_ = json.NewDecoder(r.Body).Decode(&payload)
		p.webhooks <- payload.Event + " " + payload.Key
	}))
	t.Cleanup(hook.Close)
	if _, err := s.WebhookManager.Register(hook.URL, []string{"doc.added", "doc.updated"}, "c"); err != nil {
		t.Fatal(err)
	}

	s.SSEHub = NewSSEHub(true, 10, 5)
	p.sse = &sseClient{ch: make(chan []byte, 64), mode: "readwrite"}
	s.SSEHub.clients[p.sse] = true

	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "binlog.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	s.Binlog = bl
	return p
}

// drainJobs takes every queued embedding job, runs it through the worker,
// and returns the ones for the probe's document.
func (p *writeProbe) drainJobs() []EmbeddingJob {
	var mine []EmbeddingJob
	for {
		select {
		case job := <-p.worker.jobs:
			p.worker.run(job)
			if job.DocID == p.docID {
				mine = append(mine, job)
			}
		default:
			return mine
		}
	}
}

// waitEvent waits for an event naming the probe's document.
func waitEvent(t *testing.T, what string, next func() (string, bool), want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("no %s %q for %s", what, want, parityKey)
		default:
		}
		if got, ok := next(); ok && strings.Contains(got, want) && strings.Contains(got, parityKey) {
			return
		}
	}
}

// expectEvents checks that the write fired webhook and SSE event.
func (p *writeProbe) expectEvents(t *testing.T, event string) {
	t.Helper()
	recv := func(ch chan string) func() (string, bool) {
		return func() (string, bool) {
			select {
			case v := <-ch:
				return v, true
			case <-time.After(10 * time.Millisecond):
				return "", false
			}
		}
	}
	waitEvent(t, "webhook", recv(p.webhooks), event)
	sse := make(chan string, 64)
	waitEvent(t, "SSE event", func() (string, bool) {
		select {
		case msg := <-p.sse.ch:
			sse <- string(msg)
		default:
		}
		return recv(sse)()
	}, event)
}

// drainEvents forgets the events of the setup writes.
func (p *writeProbe) drainEvents() {
	time.Sleep(50 * time.Millisecond) // webhooks are delivered by goroutines
	for {
		select {
		case <-p.webhooks:
		case <-p.sse.ch:
		default:
			return
		}
	}
}

// binlogged reports whether the binlog recorded the document since lsn.
func (p *writeProbe) binlogged(t *testing.T, lsn uint64) bool {
	t.Helper()
	entries, err := p.s.Binlog.ReadFrom(lsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.BucketName == "docs" && bytes.Equal(e.Key, storage.DocKey("c", p.docID)) {
			return true
		}
	}
	return false
}

// docVersion is what one write sets.
type docVersion struct {
	content string
	meta    map[string][]string
}

// writePath is one transport's way of writing a version of the document.
// canBlank is false for UpdateBatch, which reads empty content as "leave it".
type writePath struct {
	name     string
	canBlank bool
	write    func(t *testing.T, s *Server, v docVersion)
}

func gqlMeta(meta map[string][]string) []*gql.MetaInput {
	out := make([]*gql.MetaInput, 0, len(meta))
	for k, v := range meta {
		out = append(out, &gql.MetaInput{Key: k, Values: v})
	}
	return out
}

func mustOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func mustStatus(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("%s: %d %s", what, rec.Code, rec.Body.String())
	}
}

var writePaths = []writePath{
	{"REST add", true, func(t *testing.T, s *Server, v docVersion) {
		mustStatus(t, "add", doRequest(t, s.handleAdd, map[string]any{"collection": "c", "key": parityKey, "lang": "en", "contentMd": v.content, "meta": v.meta}))
	}},
	{"REST PATCH", true, func(t *testing.T, s *Server, v docVersion) {
		body, _ := json.Marshal(map[string]any{"collection": "c", "key": parityKey, "lang": "en", "contentMd": v.content, "meta": v.meta})
		rec := httptest.NewRecorder()
		s.handleUpdate(rec, httptest.NewRequest(http.MethodPatch, "/v1/update", bytes.NewReader(body)))
		mustStatus(t, "PATCH", rec)
	}},
	{"REST batch", true, func(t *testing.T, s *Server, v docVersion) {
		mustStatus(t, "batch", doRequest(t, s.handleAddBatch, map[string]any{"collection": "c",
			"documents": []map[string]any{{"key": parityKey, "lang": "en", "contentMd": v.content, "meta": v.meta}}}))
	}},
	{"ingest", false, func(t *testing.T, s *Server, v docVersion) {
		resp, err := s.ingestDocuments(context.Background(), "c", []IngestDocumentHTTP{{Key: parityKey, Lang: "en", ContentMD: v.content, Meta: v.meta}}, IngestOptionsHTTP{})
		mustOK(t, "ingest", err)
		if resp.Updated+resp.Added != 1 {
			t.Fatalf("ingest: %+v", resp)
		}
	}},
	{"scheduled sync (addDocument)", true, func(t *testing.T, s *Server, v docVersion) {
		_, _, err := s.addDocument("c", parityKey, "en", v.meta, v.content, 0, true)
		mustOK(t, "sync write", err)
	}},
	{"gRPC Add", true, func(t *testing.T, s *Server, v docVersion) {
		_, err := NewGRPCServer(s).Add(context.Background(), &proto.AddRequest{Collection: "c", Key: parityKey, Lang: "en", ContentMd: v.content, Meta: protoMetaOf(v.meta), SaveRevision: true})
		mustOK(t, "gRPC Add", err)
	}},
	{"gRPC UpdateDocument", true, func(t *testing.T, s *Server, v docVersion) {
		_, err := NewGRPCServer(s).UpdateDocument(context.Background(), &proto.UpdateDocumentRequest{Collection: "c", Key: parityKey, Lang: "en",
			ContentMd: v.content, UpdateContent: true, Meta: protoMetaOf(v.meta), UpdateMeta: true})
		mustOK(t, "gRPC UpdateDocument", err)
	}},
	{"gRPC AddBatch", true, func(t *testing.T, s *Server, v docVersion) {
		_, err := NewGRPCServer(s).AddBatch(context.Background(), &proto.AddBatchRequest{Collection: "c",
			Documents: []*proto.BatchDocument{{Key: parityKey, Lang: "en", ContentMd: v.content, Meta: protoMetaOf(v.meta)}}})
		mustOK(t, "gRPC AddBatch", err)
	}},
	{"gRPC UpdateBatch", false, func(t *testing.T, s *Server, v docVersion) {
		resp, err := NewGRPCServer(s).UpdateBatch(context.Background(), &proto.UpdateBatchRequest{Collection: "c",
			Documents: []*proto.UpdateDocument{{Key: parityKey, Lang: "en", ContentMd: v.content, Meta: protoMetaOf(v.meta)}}})
		mustOK(t, "gRPC UpdateBatch", err)
		if resp.Updated != 1 {
			t.Fatalf("gRPC UpdateBatch: %+v", resp)
		}
	}},
	{"MCP add_document", true, func(t *testing.T, s *Server, v docVersion) {
		_, err := NewDirectClient(s).Add(context.Background(), &MCPAddRequest{Collection: "c", Key: parityKey, Lang: "en", ContentMD: v.content, Meta: v.meta})
		mustOK(t, "MCP add", err)
	}},
	{"MCP update_document", true, func(t *testing.T, s *Server, v docVersion) {
		content := v.content
		_, err := NewDirectClient(s).UpdateDocument(context.Background(), &MCPUpdateDocumentRequest{Collection: "c", Key: parityKey, Lang: "en", ContentMD: &content, Meta: v.meta})
		mustOK(t, "MCP update", err)
	}},
	{"MCP update_batch", false, func(t *testing.T, s *Server, v docVersion) {
		resp, err := NewDirectClient(s).UpdateBatch(context.Background(), &MCPUpdateBatchRequest{Collection: "c",
			Documents: []MCPUpdateDocument{{Key: parityKey, Lang: "en", ContentMD: v.content, Meta: v.meta}}})
		mustOK(t, "MCP update_batch", err)
		if resp.Updated != 1 {
			t.Fatalf("MCP update_batch: %+v", resp)
		}
	}},
	{"GraphQL addDocument", true, func(t *testing.T, s *Server, v docVersion) {
		_, err := (&GraphQLAdapter{server: s, mcp: NewDirectClient(s)}).AddDocument(context.Background(),
			gql.AddDocumentInput{Collection: "c", Key: parityKey, Lang: "en", ContentMd: v.content, Meta: gqlMeta(v.meta)})
		mustOK(t, "GraphQL add", err)
	}},
	{"GraphQL updateDocument", true, func(t *testing.T, s *Server, v docVersion) {
		content := v.content
		_, err := (&GraphQLAdapter{server: s, mcp: NewDirectClient(s)}).UpdateDocument(context.Background(),
			gql.UpdateDocumentInput{Collection: "c", Key: parityKey, Lang: "en", ContentMd: &content, Meta: gqlMeta(v.meta)})
		mustOK(t, "GraphQL update", err)
	}},
}

var parityOriginal = docVersion{"the quokka smiles on rottnest island",
	map[string][]string{"title": {"aurora"}, "geo_lat": {"10"}, "geo_lng": {"10"}}}

// setup writes the original version and forgets the effects of doing so.
func (p *writeProbe) setup(t *testing.T) {
	t.Helper()
	_, _, err := p.s.addDocument("c", parityKey, "en", parityOriginal.meta, parityOriginal.content, 0, true)
	mustOK(t, "setup", err)
	if len(p.drainJobs()) != 1 {
		t.Fatal("setup: the add queued no embedding")
	}
	if rec, _ := p.s.VectorStore.Get("c", p.docID); rec == nil {
		t.Fatal("setup: the document has no vector")
	}
	p.drainEvents()
}

// The same update through every write path: the same effects.
func TestEveryWritePathFiresTheSameHooks(t *testing.T) {
	update := docVersion{"the wombat digs a burrow",
		map[string][]string{"title": {"zenith"}, "geo_lat": {"50"}, "geo_lng": {"20"}}}
	for _, path := range writePaths {
		t.Run(path.name, func(t *testing.T) {
			p := newWriteProbe(t)
			p.setup(t)
			lsn := p.s.Binlog.CurrentLSN()

			path.write(t, p.s, update)

			if ftsHits(t, p.s, "c", "wombat") != 1 || ftsHits(t, p.s, "c", "quokka") != 0 {
				t.Error("FTS: the index does not describe the new content")
			}
			if titleHits(t, p.s, "meta.title", "zenith") != 1 || titleHits(t, p.s, "meta.title", "aurora") != 0 {
				t.Error("FTS: the index does not describe the new metadata")
			}
			jobs := p.drainJobs()
			if len(jobs) != 1 || jobs[0].ContentMD != update.content {
				t.Errorf("embedding queue: %d jobs (%+v), want one for the new content", len(jobs), jobs)
			}
			if lat, _, ok, err := p.s.GeoStore.Get("c", p.docID); err != nil || !ok || lat != 50 {
				t.Errorf("geo: lat %v ok %v err %v, want the new point", lat, ok, err)
			}
			if hits := p.s.GeoIndex.Search("c", 50, 20, 1000, 10, nil); len(hits) != 1 {
				t.Errorf("geo index: %d documents near the new point", len(hits))
			}
			if !p.binlogged(t, lsn) {
				t.Error("binlog: the write was not recorded for replication")
			}
			p.expectEvents(t, "doc.updated")
		})
	}
}

// Content emptied through every path that can express it: the old text
// finds the document neither by full-text nor by vector, its metadata does,
// and the rest of the pipeline runs as for any update.
func TestEveryWritePathEmptiesContent(t *testing.T) {
	for _, path := range writePaths {
		if !path.canBlank {
			continue
		}
		t.Run(path.name, func(t *testing.T) {
			p := newWriteProbe(t)
			p.setup(t)
			lsn := p.s.Binlog.CurrentLSN()

			path.write(t, p.s, docVersion{"", parityOriginal.meta})

			if ftsHits(t, p.s, "c", "quokka") != 0 {
				t.Error("FTS: the emptied document is still found by its old content")
			}
			if titleHits(t, p.s, "meta.title", "aurora") != 1 {
				t.Error("FTS: the emptied document lost its metadata postings")
			}
			jobs := p.drainJobs()
			if len(jobs) != 1 || jobs[0].ContentMD != "" {
				t.Errorf("embedding queue: %+v, want one job for the empty content", jobs)
			}
			if rec, _ := p.s.VectorStore.Get("c", p.docID); rec != nil {
				t.Error("vectors: the emptied document kept its old vectors")
			}
			if n := p.s.VectorIndex.CollectionSize("c"); n != 0 {
				t.Errorf("vector index: %d vectors left, want none", n)
			}
			if !p.binlogged(t, lsn) {
				t.Error("binlog: the write was not recorded for replication")
			}
			p.expectEvents(t, "doc.updated")
		})
	}
}
