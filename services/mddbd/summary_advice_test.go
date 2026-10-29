package main

import (
	"context"
	"strings"
	"testing"

	proto "mddb/proto"
)

// RAG-009: the advisor reports how many documents carry a summary, the
// evaluation measures whether blend or expand helps, and the profile refuses
// settings that mean nothing.

func TestSummaryAdvisor_ReportsTheShare(t *testing.T) {
	s, _, cleanup := summaryServer(t, nil)
	defer cleanup()
	rec, err := s.RecommendSearch("sum")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Profile.SummaryDocuments != 2 || rec.Profile.SummaryRatio != 0.5 {
		t.Fatalf("profile: %+v", rec.Profile)
	}
	if rec.SummaryMode != SummaryModeBlend || rec.RetrievalProfile.SummaryMode != SummaryModeBlend {
		t.Fatalf("half the documents have a summary: %+v", rec)
	}
	g := &GRPCServer{server: s}
	resp, err := g.SearchAdvisor(context.Background(), &proto.SearchAdvisorRequest{Collection: "sum"})
	if err != nil || resp.Profile.SummaryDocuments != 2 || resp.Profile.SummaryRatio != 0.5 || resp.SummaryMode != SummaryModeBlend {
		t.Fatalf("grpc: %v %+v", err, resp)
	}
}

func TestSummaryAdvisor_Rules(t *testing.T) {
	few := buildRecommendation(&CollectionProfile{Documents: 10, Sampled: 10, EmbeddedDocuments: 10, MedianWords: 200,
		SummaryDocuments: 2, SummaryRatio: 0.2}, true)
	if few.SummaryMode != "" || !strings.Contains(strings.Join(few.Reasons, " "), "summaryMode left off") {
		t.Fatalf("few summaries: %+v", few)
	}
	keyword := buildRecommendation(&CollectionProfile{Documents: 10, Sampled: 10, MedianWords: 200,
		SummaryDocuments: 9, SummaryRatio: 0.9}, false)
	if keyword.SummaryMode != "" || !strings.Contains(strings.Join(keyword.Reasons, " "), "bm25f") {
		t.Fatalf("keyword only: %+v", keyword)
	}
	none := buildRecommendation(&CollectionProfile{Documents: 10, Sampled: 10, EmbeddedDocuments: 10, MedianWords: 200}, true)
	if none.SummaryMode != "" || strings.Contains(strings.Join(none.Reasons, " "), "summar") {
		t.Fatalf("no summaries: %+v", none)
	}
}

func TestSummaryEval_MeasuresTheModes(t *testing.T) {
	s, _, cleanup := summaryServer(t, nil)
	defer cleanup()
	q := []EvalQuery{{ID: "g", Query: generalQuery + " tomatoes", ExpectedKeys: []string{"plant"}}}
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "sum", K: 1, Queries: q, Configs: []EvalConfig{
		{SearchType: SearchTypeVector}, {SearchType: SearchTypeVector, SummaryMode: SummaryModeBlend},
		{SearchType: SearchTypeHybrid, SummaryMode: SummaryModeExpand, Alpha: floatPtr(1)},
		{SearchType: SearchTypeFTS, Algorithm: "bm25f", SummaryMode: SummaryModeBlend},
		{SearchType: SearchTypeVector, SummaryMode: "sometimes"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results
	if r[0].Recall != 0 || r[1].Recall != 1 || r[2].Recall != 1 || r[3].Recall != 1 {
		t.Fatalf("recall off/blend/expand/bm25f: %v %v %v %v", r[0].Recall, r[1].Recall, r[2].Recall, r[3].Recall)
	}
	if r[1].Config != "vector summary=blend" || r[4].Error == "" {
		t.Fatalf("label %q, bad mode error %q", r[1].Config, r[4].Error)
	}
	g := &GRPCServer{server: s}
	out, err := g.RunEval(context.Background(), &proto.RunEvalRequest{Collection: "sum", K: 1,
		Queries: []*proto.EvalQueryProto{{Id: "g", Query: generalQuery + " tomatoes", ExpectedKeys: []string{"plant"}}},
		Configs: []*proto.EvalConfigProto{{SearchType: "vector", SummaryMode: SummaryModeBlend}}})
	if err != nil || out.Results[0].RecallAtK != 1 {
		t.Fatalf("grpc: %v %+v", err, out)
	}
}

func TestSummaryProfile_Validation(t *testing.T) {
	for _, p := range []*RetrievalProfileDef{
		{SummaryMode: "sometimes"},
		{SummaryWeight: floatPtr(-1)},
		{SummaryWeight: floatPtr(maxSummaryWeight + 1)},
	} {
		if err := p.Validate(); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
	for _, p := range []*RetrievalProfileDef{{SummaryMode: SummaryModeOff, SummaryWeight: floatPtr(0)}, {SummaryMode: SummaryModeExpand}} {
		if err := p.Validate(); err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	w := 3.5
	back := retrievalProfileFromProto(retrievalProfileToProto(&RetrievalProfileDef{SummaryMode: SummaryModeBlend, SummaryWeight: &w}))
	if back.SummaryMode != SummaryModeBlend || *back.SummaryWeight != w {
		t.Fatalf("proto round trip: %+v", back)
	}
	s := &Server{}
	if s.ResolveSummaryMode("c", SummaryModeOff) != "" || s.ResolveSummaryMode("c", SummaryModeBlend) != SummaryModeBlend {
		t.Fatal("override")
	}
	if got := s.summaryFieldWeights("c", "", map[string]float64{"content": 1}); len(got) != 1 {
		t.Fatalf("off: %v", got)
	}
	var p *SearchPlan
	if p.expandedChunks("d") != nil {
		t.Fatal("nil plan")
	}
}
