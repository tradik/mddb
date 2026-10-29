package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/fts"
	"mddb/internal/tracing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// OpenTelemetry trace export (OPS-024).
//
// One set of stage boundaries, recorded once: each stage below starts a span
// under the request's server span, and the exporter sends them to the
// collector MDDB_OTEL_ENDPOINT names. The explain plan (SRCH-013) says which
// index a search used and why; the spans say how long each stage took. Both
// describe the same stages — the names here are the vocabulary for both.
//
// Off by default and free when off: s.Tracer is nil, every Stage returns a nil
// span, and the HTTP and gRPC chains are not wrapped at all.

// The stages a search or an ingest is made of.
const (
	spanSearchFTS         = "search.fts"          // the full-text half
	spanSearchEmbedQuery  = "search.embed_query"  // embedding the query text
	spanSearchVector      = "search.vector"       // the vector index search, planner included
	spanSearchFuse        = "search.fuse"         // merging the halves: alpha, rrf, weighted
	spanSearchLoadContent = "search.load_content" // reading the hit documents
	spanIngestConvert     = "ingest.convert"      // keys, frontmatter, metadata, dedup
	spanIngestIndex       = "ingest.index"        // the batch write and its indexes
	spanIngestEmbed       = "ingest.embed"        // one document's embedding, on the worker
)

// traceparentHeader is the W3C trace context header, and the gRPC metadata
// key that carries it (gRPC lowercases metadata keys).
const traceparentHeader = "traceparent"

// tracerShutdownTimeout bounds the last export at shutdown.
// A variable only so a test need not wait five seconds.
var tracerShutdownTimeout = 5 * time.Second

// initTracing starts the exporter when MDDB_OTEL_ENDPOINT is set. Drops and
// failed exports are counted in mddb_operations_total, next to everything
// else Prometheus scrapes.
func (s *Server) initTracing() {
	cfg := tracing.FromEnv(VERSION)
	cfg.OnDrop = func() { s.Metrics.IncOp("otel_spans_dropped") }
	cfg.OnExportError = func() { s.Metrics.IncOp("otel_export_failed") }
	s.Tracer = tracing.New(cfg)
	if s.Tracer != nil {
		slog.Info("OpenTelemetry trace export enabled", "endpoint", cfg.Endpoint, "sampleRatio", cfg.SampleRatio)
	}
	if s.EmbeddingWorker != nil {
		s.EmbeddingWorker.tracer = s.Tracer
	}
}

// shutdownTracing sends the spans still queued.
func (s *Server) shutdownTracing() {
	ctx, cancel := context.WithTimeout(context.Background(), tracerShutdownTimeout)
	defer cancel()
	if err := s.Tracer.Shutdown(ctx); err != nil {
		slog.Warn("OpenTelemetry spans not exported before shutdown", "err", err)
	}
}

// withTracing gives every HTTP request a server span, continuing the caller's
// trace when it sent a traceparent. With tracing off it returns next itself.
func (s *Server) withTracing(next http.Handler) http.Handler {
	if s.Tracer == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, sp := s.Tracer.StartServer(r.Context(), r.Method+" "+r.URL.Path, r.Header.Get(traceparentHeader))
		defer sp.End()
		sp.SetString("http.request.method", r.Method)
		sp.SetString("url.path", r.URL.Path)
		rec := &accessLogRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))
		sp.SetInt("http.response.status_code", rec.status)
		if rec.status >= http.StatusInternalServerError {
			sp.SetError(errHTTPStatus(rec.status))
		}
	})
}

// errHTTPStatus is a 5xx response as the error a span reports.
type errHTTPStatus int

func (e errHTTPStatus) Error() string { return http.StatusText(int(e)) }

// tracingUnaryInterceptor gives every unary RPC a server span, continuing the
// trace in the call's traceparent metadata.
func (s *Server) tracingUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var parent string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get(traceparentHeader); len(v) > 0 {
				parent = v[0]
			}
		}
		ctx, sp := s.Tracer.StartServer(ctx, info.FullMethod, parent)
		defer sp.End()
		sp.SetString("rpc.system", "grpc")
		sp.SetString("rpc.method", info.FullMethod)
		resp, err := handler(ctx, req)
		sp.SetError(err)
		return resp, err
	}
}

// requestTrace is the span ctx carries, for work queued past the response;
// zero when tracing is off.
func (s *Server) requestTrace(ctx context.Context) tracing.SpanContext {
	if s.Tracer == nil {
		return tracing.SpanContext{}
	}
	return tracing.FromContext(ctx).Context()
}

// tracedFTS is the full-text half of a hybrid search as a search.fts stage.
func (s *Server) tracedFTS(ctx context.Context, req HybridSearchRequest) ([]fts.FTSResult, error) {
	sp := s.Tracer.Stage(ctx, spanSearchFTS)
	defer sp.End()
	sp.SetString("mddb.collection", req.Collection)
	sp.SetString("mddb.fts.algorithm", req.Algorithm)
	results, err := s.runFTSSearch(req)
	sp.SetInt("mddb.results", len(results))
	sp.SetError(err)
	return results, err
}

// embedQueryTimeout bounds embedding one query on the search path.
const embedQueryTimeout = 15 * time.Second

// embedQuery embeds a search query as a search.embed_query stage: the one
// place the vector paths turn query text into a vector.
func (s *Server) embedQuery(ctx context.Context, query string) ([]float32, error) {
	sp := s.Tracer.Stage(ctx, spanSearchEmbedQuery)
	defer sp.End()
	embedCtx, cancel := context.WithTimeout(ctx, embedQueryTimeout)
	defer cancel()
	v, err := s.Embedding.Embed(embedCtx, query, embedding.RoleQuery)
	sp.SetError(err)
	return v, err
}

// tracedLoadHybridDocs reads a hybrid search's hits as a search.load_content
// stage.
func (s *Server) tracedLoadHybridDocs(ctx context.Context, collection string, items []HybridSearchResultItem, includeContent bool) []HybridSearchResultItem {
	sp := s.Tracer.Stage(ctx, spanSearchLoadContent)
	defer sp.End()
	out := s.loadHybridDocs(collection, items, includeContent)
	sp.SetInt("mddb.documents", len(out))
	return out
}
