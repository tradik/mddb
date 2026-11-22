package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	json "mddb/internal/jsonx"
	"mddb/internal/rerank"
)

// #450: POST /v1/rerank as Open WebUI's external reranker calls it.

// stubReranker scores by a fixed answer and remembers what it was sent.
type stubReranker struct {
	answer []rerank.Scored
	err    error
	got    []string
}

func (f *stubReranker) Rerank(_ context.Context, _ string, docs []string, _ int) ([]rerank.Scored, error) {
	f.got = docs
	return f.answer, f.err
}

func (f *stubReranker) Model() string { return "cross-encoder-test" }

// rerankCall posts body to /v1/rerank as c.
func rerankCall(s *Server, c *JWTClaims, body string) (int, rerankAPIResponse, string) {
	rec := owuiCall(s, c, http.MethodPost, "/v1/rerank", strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
	var out rerankAPIResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestRerankAPIAnswersOneScorePerDocument(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	switchModules(t, s, FeatureRerankAPI)
	// The model scores two of three, best first; the third still gets a score.
	fake := &stubReranker{answer: []rerank.Scored{{Index: 2, Score: 0.9}, {Index: 0, Score: 0.4}}}
	s.setRerank(fake, 50, "test")

	long := strings.Repeat("ł", maxRerankChars+10)
	code, res, body := rerankCall(s, nil, fmt.Sprintf(`{"model":"reranker","query":"oil","documents":["a","%s","c"],"top_n":3}`, long))
	if code != http.StatusOK || res.Model != "cross-encoder-test" || len(res.Results) != 3 {
		t.Fatalf("%d %s", code, body)
	}
	want := []rerankAPIResult{{2, 0.9}, {0, 0.4}, {1, -1}}
	for i, r := range res.Results {
		if r != want[i] {
			t.Errorf("result %d: %+v, want %+v", i, r, want[i])
		}
	}
	if utf8.RuneCountInString(fake.got[1]) != maxRerankChars {
		t.Errorf("a document is read up to %d characters, sent %d", maxRerankChars, utf8.RuneCountInString(fake.got[1]))
	}

	// top_n cuts; negative scores keep the floor below them.
	fake.answer = []rerank.Scored{{Index: 1, Score: -2}}
	if _, res, _ := rerankCall(s, nil, `{"query":"oil","documents":["a","b"],"top_n":1}`); len(res.Results) != 1 || res.Results[0].Index != 1 {
		t.Errorf("top_n 1: %+v", res.Results)
	}
	if _, res, _ := rerankCall(s, nil, `{"query":"oil","documents":["a","b"]}`); res.Results[1] != (rerankAPIResult{0, -3}) {
		t.Errorf("floor under a negative score: %+v", res.Results)
	}
	fake.answer = nil
	if code, res, _ := rerankCall(s, nil, `{"query":"oil","documents":[]}`); code != http.StatusOK || len(res.Results) != 0 {
		t.Errorf("no documents: %d %+v", code, res)
	}
}

func TestRerankAPIRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	switchModules(t, s, FeatureRerankAPI)
	many := `"` + strings.Repeat(`x","`, rerankAPIMaxDocuments) + `x"`
	big := strings.Repeat("y", rerankAPIMaxBytes/2+1)
	cases := []struct {
		body string
		code int
		want string
	}{
		{`{`, http.StatusBadRequest, "invalid JSON body"},
		{`{"documents":["a"]}`, http.StatusBadRequest, "query is required"},
		{`{"query":"q","documents":[` + many + `]}`, http.StatusBadRequest, "too many documents: 201, at most 200"},
		{`{"query":"q","documents":["` + big + `","` + big + `"]}`, http.StatusBadRequest, "documents too large"},
		{`{"query":"q","documents":["a"],"top_n":-1}`, http.StatusBadRequest, "top_n must be 0 or more"},
		{`{"query":"q","documents":["a"]}`, http.StatusServiceUnavailable, "no reranking model is configured"},
	}
	for _, tc := range cases {
		if code, _, body := rerankCall(s, nil, tc.body); code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%.60s: %d %s", tc.body, code, body)
		}
	}
	s.setRerank(&stubReranker{err: errors.New("upstream said no")}, 50, "test")
	if code, _, body := rerankCall(s, nil, `{"query":"q","documents":["a"]}`); code != http.StatusBadGateway ||
		body != `{"error":"the reranking model failed: error"}` {
		t.Errorf("provider error: %d %s", code, body)
	}
}

// Any credential may call it; none may not.
func TestRerankAPIIsForAuthenticatedCallers(t *testing.T) {
	s := accessServer(t)
	switchModules(t, s, FeatureRerankAPI)
	s.setRerank(&stubReranker{answer: []rerank.Scored{{Index: 0, Score: 1}}}, 50, "test")
	if code, _, body := rerankCall(s, nil, `{"query":"q","documents":["a"]}`); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d %s", code, body)
	}
	for _, c := range []*JWTClaims{alice, tina} {
		if code, _, body := rerankCall(s, c, `{"query":"q","documents":["a"]}`); code != http.StatusOK {
			t.Errorf("%s: %d %s", c.Username, code, body)
		}
	}
}
