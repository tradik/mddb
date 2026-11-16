package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	proto "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GO-052: what spans groups — cross-search, stats, global settings — and
// the other surfaces' routing.

// placedHit is what is compared between a fan-out and one node.
type placedHit struct {
	Collection string
	Key        string
	Score      float32
	Rank       int
}

func placedHits(t *testing.T, raw []byte) []placedHit {
	t.Helper()
	var resp struct {
		Results []struct {
			Collection string `json:"collection"`
			Document   struct {
				Key string `json:"key"`
			} `json:"document"`
			Score float32 `json:"score"`
			Rank  int     `json:"rank"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	out := make([]placedHit, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, placedHit{r.Collection, r.Document.Key, r.Score, r.Rank})
	}
	return out
}

// testVec is a small deterministic vector for document d of collection c.
func testVec(c string, d int) []float32 {
	h := len(c)*31 + d*17
	return []float32{float32(h%7) + 1, float32(h%5) + 1, float32(d%3) + 1, float32(len(c)%4) + 1}
}

func TestPlacementCrossSearchAnswersAsOneNodeWould(t *testing.T) {
	tp := threeGroups(t)
	one, cleanup := newHandlerTestServer(t)
	defer cleanup()
	var targets []string
	for i := range 9 {
		c := fmt.Sprintf("cs-%d", i)
		targets = append(targets, c)
		seedCrossCollection(t, c, tp.leader(mustOwner(t, tp, c)).s, one)
	}
	one.VectorIndex.SetReady()
	oneMux := http.NewServeMux()
	one.registerRoutes(oneMux, false)
	bodies := []map[string]any{
		{"targetCollections": targets, "query": "alpha", "searchType": "fts", "topK": 7},
		{"targetCollections": targets, "queryVector": []float32{2, 3, 1, 2}, "topK": 8},
		{"targetCollections": targets, "queryVector": []float32{2, 3, 1, 2}, "topK": 5,
			"groupBy": map[string]any{"key": "collection", "perGroup": 1}},
	}
	for i, body := range bodies {
		want := oneNodeHits(t, oneMux, body)
		for _, n := range []*placementNodeT{tp.nodes[0], tp.nodes[len(tp.nodes)-1]} {
			var got json.RawMessage
			if _, err := n.call(http.MethodPost, "/v1/cross-search", body, &got); err != nil {
				t.Fatal(err)
			}
			if hits := placedHits(t, got); !reflect.DeepEqual(hits, want) {
				t.Fatalf("case %d through %s:\n got %v\nwant %v", i, n.id, hits, want)
			}
		}
	}
	// Feedback examples are vectors of other groups' documents: refused.
	_, err := tp.nodes[0].call(http.MethodPost, "/v1/cross-search", map[string]any{"targetCollections": targets,
		"queryVector": []float32{1, 1, 1, 1}, "positive": []map[string]string{{"collection": targets[0], "key": "k1"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("feedback over groups: %v", err)
	}
}

// seedCrossCollection writes the same six documents, with vectors, to c on
// every server.
func seedCrossCollection(t *testing.T, c string, servers ...*Server) {
	t.Helper()
	for d := range 6 {
		key, text := fmt.Sprintf("k%d", d), fmt.Sprintf("beta %s gamma%d", strings.Repeat("alpha ", d+1), d)
		for _, s := range servers {
			saved, _, err := s.addDocument(c, key, "en", nil, text, 0, true)
			if err == nil {
				err = s.VectorStore.Put(c, saved.ID, testVec(c, d), "test", "")
			}
			if err == nil {
				err = (serverIndexes{s}).IndexChunk(c, saved.ID, testVec(c, d))
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// oneNodeHits is what the node holding every target answers to body.
func oneNodeHits(t *testing.T, mux http.Handler, body map[string]any) []placedHit {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/cross-search", strings.NewReader(string(raw))))
	want := placedHits(t, rec.Body.Bytes())
	if len(want) == 0 {
		t.Fatalf("one node found nothing: %s", rec.Body.String())
	}
	return want
}

func TestPlacementStatsCountEveryGroupOnce(t *testing.T) {
	tp := threeGroups(t)
	names := writeCollections(t, tp, 12, 2)
	for _, n := range tp.nodes {
		var stats struct {
			Collections []struct {
				Name string `json:"name"`
			} `json:"collections"`
			TotalDocuments float64 `json:"totalDocuments"`
			Groups         []statsGroup
		}
		waitE2E(t, "stats through "+n.id, func() bool {
			if _, err := n.call(http.MethodGet, "/v1/stats", nil, &stats); err != nil {
				t.Fatal(err)
			}
			return len(stats.Collections) == len(names)
		})
		if stats.TotalDocuments != float64(2*len(names)) || len(stats.Groups) != 3 {
			t.Fatalf("through %s: %v documents, groups %+v", n.id, stats.TotalDocuments, stats.Groups)
		}
	}
}

func TestPlacementGlobalSettingsLiveInTheMetaGroup(t *testing.T) {
	tp := startTestPlacementWith(t, map[string]int{"meta": 1, "g2": 1, "g3": 1}, func(s *Server) {
		s.Aliases = NewAliasManager(s.DB)
		if err := s.Aliases.LoadAll(); err != nil {
			t.Fatal(err)
		}
		s.Aliases.SetBinlog(s.Binlog)
	})
	target := "aliased-target"
	owner := mustOwner(t, tp, target)
	if err := tp.leader(owner).add(target, "k", "aliased text"); err != nil {
		t.Fatal(err)
	}
	// Written through a node of another group: it goes to the meta leader,
	// and every group's leader copies it.
	via := tp.leader(otherGroup(owner))
	if _, err := via.call(http.MethodPut, "/v1/aliases/nick", map[string]string{"collection": target}, nil); err != nil {
		t.Fatal(err)
	}
	if got := tp.leader("meta").s.Aliases.Resolve("nick"); got != target {
		t.Fatalf("the meta leader resolves the alias to %q", got)
	}
	for _, n := range tp.nodes {
		waitE2E(t, n.id+" to learn the alias", func() bool { return n.s.Aliases.Resolve("nick") == target })
	}
	for _, n := range tp.nodes {
		var doc struct {
			Key string `json:"key"`
		}
		if _, err := n.call(http.MethodPost, "/v1/get", map[string]string{"collection": "nick", "key": "k", "lang": "en"}, &doc); err != nil || doc.Key != "k" {
			t.Fatalf("reading through the alias via %s: %v %+v", n.id, err, doc)
		}
	}
}

func TestPlacementGRPCCallsReachTheGroupHoldingTheCollection(t *testing.T) {
	tp := threeGroups(t)
	const c = "over-grpc"
	owner := mustOwner(t, tp, c)
	var via *placementNodeT
	for _, n := range tp.nodes {
		if n.group != owner {
			via = n
		}
	}
	conn, err := grpc.NewClient(via.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := proto.NewMDDBClient(conn)
	if _, err := client.Add(context.Background(), &proto.AddRequest{Collection: c, Key: "g", Lang: "en", ContentMd: "over grpc"}); err != nil {
		t.Fatal(err)
	}
	doc, err := client.Get(context.Background(), &proto.GetRequest{Collection: c, Key: "g", Lang: "en"})
	if err != nil || doc.Key != "g" {
		t.Fatalf("get through %s: %v %v", via.id, doc, err)
	}
	if _, count := tp.leader(owner).s.collectionChecksum(c); count != 1 || via.holds(c) {
		t.Fatalf("the owner holds %d documents; the node called holds a copy: %v", count, via.holds(c))
	}
}

func TestPlacementRedirectModeNamesTheGroupLeader(t *testing.T) {
	tp := startTestPlacementWith(t, map[string]int{"meta": 1, "g2": 1}, func(s *Server) {})
	for _, n := range tp.nodes {
		n.s.placement.cfg.redirect = true
	}
	c := "redirected"
	owner := mustOwner(t, tp, c)
	var via *placementNodeT
	for _, n := range tp.nodes {
		if n.group != owner {
			via = n
		}
	}
	req, _ := http.NewRequest(http.MethodPost, via.http.URL+"/v1/add",
		strings.NewReader(`{"collection":"redirected","key":"a","lang":"en","contentMd":"x"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest || resp.Header.Get(hdrPlacementGroup) != owner ||
		resp.Header.Get("X-MDDB-Leader") != tp.leader(owner).http.URL {
		t.Fatalf("redirect: %d group %q leader %q", resp.StatusCode, resp.Header.Get(hdrPlacementGroup), resp.Header.Get("X-MDDB-Leader"))
	}
}
