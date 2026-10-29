package main

import (
	"context"

	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gRPC for the 2.18.8 agent features: symbol lookup (CODE-008), retrieval
// evaluation (SRCH-019) and retrieval gain (AGT-004). Each calls the same
// server function as its REST endpoint and MCP tool.

// permit checks a permission on a collection, as a gRPC status.
func (g *GRPCServer) permit(ctx context.Context, collection string, perm PermissionType) error {
	if g.server.AuthManager == nil {
		return nil
	}
	if err := g.server.AuthManager.CheckPermission(ctx, collection, perm); err != nil {
		return status.Error(codes.PermissionDenied, err.Error())
	}
	return nil
}

// CodeSymbol answers where a symbol is defined and used (CODE-008).
func (g *GRPCServer) CodeSymbol(ctx context.Context, req *proto.CodeSymbolRequest) (*proto.CodeSymbolResponse, error) {
	if err := g.permit(ctx, req.Collection, PermRead); err != nil {
		return nil, err
	}
	res, err := g.server.LookupSymbol(SymbolRequest{Collection: req.Collection, Name: req.Name, Include: req.Include,
		Limit: int(req.Limit), IncludeContent: req.IncludeContent})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &proto.CodeSymbolResponse{Collection: res.Collection, Symbol: res.Symbol,
		Definitions: passagesToProto(res.Definitions), References: passagesToProto(res.References),
		Callers: passagesToProto(res.Callers), Callees: passagesToProto(res.Callees), Tests: passagesToProto(res.Tests),
		Siblings: passagesToProto(res.Siblings), Truncated: res.Truncated}, nil
}

func evalQueryFromProto(q *proto.EvalQueryProto) EvalQuery {
	out := EvalQuery{ID: q.GetId(), Query: q.GetQuery(), ExpectedKeys: q.GetExpectedKeys()}
	if len(q.GetGrades()) > 0 {
		out.Grades = make(map[string]int, len(q.GetGrades()))
		for k, v := range q.GetGrades() {
			out.Grades[k] = int(v)
		}
	}
	return out
}

func evalQueryToProto(q EvalQuery) *proto.EvalQueryProto {
	out := &proto.EvalQueryProto{Id: q.ID, Query: q.Query, ExpectedKeys: q.ExpectedKeys}
	if len(q.Grades) > 0 {
		out.Grades = make(map[string]int32, len(q.Grades))
		for k, v := range q.Grades {
			out.Grades[k] = safeInt32(v)
		}
	}
	return out
}

// RunEval scores search configurations on the evaluation set (SRCH-019).
func (g *GRPCServer) RunEval(ctx context.Context, req *proto.RunEvalRequest) (*proto.RunEvalResponse, error) {
	if err := g.permit(ctx, req.Collection, PermRead); err != nil {
		return nil, err
	}
	er := EvalRequest{Collection: req.Collection, K: int(req.K), Details: req.Details}
	for _, c := range req.Configs {
		er.Configs = append(er.Configs, EvalConfig{Name: c.Name, SearchType: c.SearchType, Algorithm: c.Algorithm,
			VectorAlgorithm: c.VectorAlgorithm, Alpha: c.Alpha, Strategy: c.Strategy, RetrievalMode: c.RetrievalMode,
			BM25K1: c.Bm25K1, BM25B: c.Bm25B})
	}
	for _, q := range req.Queries {
		er.Queries = append(er.Queries, evalQueryFromProto(q))
	}
	res, err := g.server.RunEval(ctx, er)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	out := &proto.RunEvalResponse{Collection: res.Collection, K: safeInt32(res.K), Queries: safeInt32(res.Queries)}
	for _, r := range res.Results {
		pr := &proto.EvalConfigResultProto{Config: r.Config, RecallAtK: r.Recall, NdcgAtK: r.NDCG, Mrr: r.MRR,
			P50Ms: r.P50Ms, P99Ms: r.P99Ms, Tokens: int64(r.Tokens), Error: r.Error}
		for _, q := range r.PerQuery {
			pr.PerQuery = append(pr.PerQuery, &proto.EvalQueryResultProto{Id: q.ID, Query: q.Query, Recall: q.Recall,
				Ndcg: q.NDCG, ReciprocalRank: q.RR, Returned: q.Returned})
		}
		out.Results = append(out.Results, pr)
	}
	return out, nil
}

// AddEvalQuery stores one evaluation query.
func (g *GRPCServer) AddEvalQuery(ctx context.Context, req *proto.AddEvalQueryRequest) (*proto.EvalQueryProto, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if err := g.permit(ctx, req.Collection, PermWrite); err != nil {
		return nil, err
	}
	q, err := g.server.AddEvalQuery(req.Collection, evalQueryFromProto(req.Query))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return evalQueryToProto(q), nil
}

// ListEvalQueries returns the stored evaluation set.
func (g *GRPCServer) ListEvalQueries(ctx context.Context, req *proto.ListEvalQueriesRequest) (*proto.ListEvalQueriesResponse, error) {
	if err := g.permit(ctx, req.Collection, PermRead); err != nil {
		return nil, err
	}
	out := &proto.ListEvalQueriesResponse{Collection: req.Collection}
	for _, q := range g.server.evalSet(req.Collection) {
		out.Queries = append(out.Queries, evalQueryToProto(q))
	}
	return out, nil
}

// DeleteEvalQuery removes one evaluation query by id.
func (g *GRPCServer) DeleteEvalQuery(ctx context.Context, req *proto.DeleteEvalQueryRequest) (*proto.DeleteEvalQueryResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if err := g.permit(ctx, req.Collection, PermWrite); err != nil {
		return nil, err
	}
	found, err := g.server.DeleteEvalQuery(req.Collection, req.Id)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.DeleteEvalQueryResponse{Deleted: found}, nil
}

func gainToProto(c GainCounters) *proto.GainCountersProto {
	return &proto.GainCountersProto{Searches: c.Searches, Algorithms: c.Algorithms, Fusion: c.Fusion,
		TokensReturned: c.TokensReturned, TokensAvoided: c.TokensAvoided, FragmentHits: c.FragmentHits,
		DocumentHits: c.DocumentHits}
}

// GetGain reports what retrieval saved over a window of days (AGT-004).
func (g *GRPCServer) GetGain(ctx context.Context, req *proto.GetGainRequest) (*proto.GetGainResponse, error) {
	if err := g.permit(ctx, "*", PermRead); err != nil {
		return nil, err
	}
	days, err := parseGainWindow(req.Window)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	rep, err := g.server.Gain.Report(days, req.Daily)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	out := &proto.GetGainResponse{WindowDays: safeInt32(rep.WindowDays), FromDate: rep.From, ToDate: rep.To,
		Totals: gainToProto(rep.Totals), TotalSearches: rep.TotalSearches,
		TokensWithoutRetrieval: rep.TokensWithoutRetrieval, SavedPercent: rep.SavedPercent}
	for _, d := range rep.Daily {
		out.Daily = append(out.Daily, &proto.GainDayProto{Date: d.Date, Counters: gainToProto(d.GainCounters)})
	}
	return out, nil
}
