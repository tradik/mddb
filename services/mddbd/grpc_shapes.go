package main

import (
	"errors"

	"mddb/internal/storage"
	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Conversions between the proto messages of 2.18.8 and the shared Go types,
// so the gRPC surface runs the same search code as REST and MCP instead of a
// copy of it — the copies are why gRPC fell behind (filter, groupBy,
// passages, waits).

func metaValuesFromProto(m map[string]*proto.MetaValues) map[string][]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = v.GetValues()
	}
	return out
}

// metaFilterFromProto converts the operator filter (SRCH-017).
func metaFilterFromProto(m map[string]*proto.MetaCondition) MetaFilter {
	if len(m) == 0 {
		return nil
	}
	f := make(MetaFilter, len(m))
	for k, c := range m {
		f[k] = MetaCondition{In: c.GetAnyOf(), hasIn: len(c.GetAnyOf()) > 0, Prefix: c.GetPrefix(), Contains: c.GetContains(),
			Gt: c.Gt, Gte: c.Gte, Lt: c.Lt, Lte: c.Lte, Not: c.GetNoneOf(), Exists: c.Exists}
	}
	return f
}

// groupByOf converts a request's group_by (SRCH-016).
func groupByOf(g *proto.GroupBy) *GroupBy {
	return groupByFromProto(g.GetKey(), g.GetPerGroup())
}

func passageOptions(format string, lines int32) PassageOptions {
	return PassageOptions{Format: format, PassageLines: int(lines)}
}

func indexWait(ms int32, keys []string, level string) IndexWait {
	return IndexWait{WaitForIndex: int(ms), WaitForKeys: keys, ReadLevel: level}
}

func indexWaitToProto(w *IndexWaitResult) *proto.IndexWaitResult {
	if w == nil {
		return nil
	}
	return &proto.IndexWaitResult{WaitedMs: w.WaitedMs, TimedOut: w.TimedOut}
}

func passagesToProto(ps []Passage) []*proto.Passage {
	if len(ps) == 0 {
		return nil
	}
	out := make([]*proto.Passage, len(ps))
	for i, p := range ps {
		out[i] = &proto.Passage{Collection: p.Collection, Key: p.Key, Lang: p.Lang, Path: p.Path,
			StartLine: safeInt32(p.Lines.Start), EndLine: safeInt32(p.Lines.End), Symbol: p.Symbol, Kind: p.Kind,
			Content: p.Content, Score: p.Score, Source: p.Source, Tokens: safeInt32(p.Tokens)}
	}
	return out
}

// passagesOf returns a response's passages, if it has any.
func passagesOf(p *PassagesResponse) []*proto.Passage {
	if p == nil {
		return nil
	}
	return passagesToProto(p.Passages)
}

// mcpDocToProto converts a document from the shared MCP response shape.
func mcpDocToProto(d MCPDocument, includeContent bool) *proto.Document {
	doc := mcpDocumentToDoc(d)
	if !includeContent {
		doc.ContentMD = ""
	}
	return docToProto(&doc)
}

func docsOfMCP(docs []MCPDocument) []storage.Doc {
	out := make([]storage.Doc, len(docs))
	for i, d := range docs {
		out[i] = mcpDocumentToDoc(d)
	}
	return out
}

// errSourceNoEmbedding: a cross-search source document without a vector.
var errSourceNoEmbedding = errors.New("source document has no embedding")

// errEmbedQuery wraps a failure of the embedding provider on a query.
var errEmbedQuery = errors.New("failed to embed query")

// searchStatus maps an error from the shared search code to a gRPC status:
// a loading vector index is Unavailable, a missing embedding provider
// FailedPrecondition, anything else the caller's request (InvalidArgument).
func searchStatus(err error) error {
	var loading *vectorLoadingError
	switch {
	case errors.As(err, &loading):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, errNoEmbeddingProvider):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, errSourceNoEmbedding):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, errEmbedQuery):
		return status.Error(codes.Internal, err.Error())
	}
	return status.Error(codes.InvalidArgument, err.Error())
}
