package embedding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #252: a provider answered with a hole and called it a success.
//
// OpenAI and Voyage placed each embedding at the position its `index` named,
// in a slice sized by how many entries came back rather than how many texts
// went out. A short answer or a skipped index left nil vectors that were
// returned with a nil error — and one nil vector could cost a collection its
// whole HNSW graph.

// fakeIndexedAPI serves an OpenAI/Voyage-shaped answer built from the given
// (index, vector) pairs, so a test can reproduce exactly the malformed replies
// a rate-limited upstream produces.
func fakeIndexedAPI(t *testing.T, entries [][2]string) *httptest.Server {
	t.Helper()
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf(`{"index":%s,"embedding":%s,"object":"embedding"}`, e[0], e[1]))
	}
	body := `{"object":"list","data":[` + strings.Join(parts, ",") + `],"model":"m"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIndexedProvidersRefuseAnAnswerWithHoles(t *testing.T) {
	cases := []struct {
		name    string
		entries [][2]string
		want    string
	}{
		{"fewer embeddings than texts", [][2]string{{"0", "[0.1,0.2]"}}, "returned no embedding for text 2"},
		{"an index skipped", [][2]string{{"0", "[0.1,0.2]"}, {"2", "[0.3,0.4]"}}, "returned an embedding for text 3 of 2"},
		{"an index repeated", [][2]string{{"0", "[0.1,0.2]"}, {"0", "[0.3,0.4]"}}, "returned two embeddings for text 1"},
		{"an empty embedding", [][2]string{{"0", "[0.1,0.2]"}, {"1", "[]"}}, "returned no embedding for text 2"},
		{"lengths disagree in one batch", [][2]string{{"0", "[0.1,0.2]"}, {"1", "[0.3]"}}, "different lengths"},
	}

	providers := map[string]func(url string) Provider{
		"openai": func(url string) Provider { return NewOpenAIProvider("k", url, "m", 2) },
		"voyage": func(url string) Provider { return NewVoyageProvider("k", url, "m", 2) },
	}

	for pname, build := range providers {
		for _, tc := range cases {
			t.Run(pname+"/"+tc.name, func(t *testing.T) {
				srv := fakeIndexedAPI(t, tc.entries)
				vectors, err := build(srv.URL).EmbedBatch(context.Background(), []string{"a", "b"}, RoleDocument)
				if err == nil {
					t.Fatalf("a malformed answer was returned as a success: %v", vectors)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %q, want it to say %q", err, tc.want)
				}
			})
		}
	}
}

// The well-formed answer, including out-of-order indices, still works.
func TestIndexedProvidersPlaceByIndexWhenTheAnswerIsWhole(t *testing.T) {
	srv := fakeIndexedAPI(t, [][2]string{{"1", "[0.3,0.4]"}, {"0", "[0.1,0.2]"}})
	vectors, err := NewOpenAIProvider("k", srv.URL, "m", 2).EmbedBatch(context.Background(), []string{"a", "b"}, RoleDocument)
	if err != nil {
		t.Fatal(err)
	}
	if vectors[0][0] != 0.1 || vectors[1][0] != 0.3 {
		t.Errorf("vectors = %v, want them in the order of their inputs", vectors)
	}
}

// Ollama checked that the outer array was non-empty and returned the vector
// inside it without looking.
func TestOllamaRefusesAnEmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"m","embeddings":[[]]}`))
	}))
	defer srv.Close()

	v, err := NewOllamaProvider(srv.URL, "m", 0).Embed(context.Background(), "text", RoleDocument)
	if err == nil {
		t.Fatalf("an empty vector was returned as a success: %v", v)
	}
	if !strings.Contains(err.Error(), "returned no embedding") {
		t.Errorf("error = %q — it failed, but not on the empty vector", err)
	}
}

func TestCohereRefusesAnEmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The shape Cohere actually answers with, so the decode succeeds and
		// the check under test is the one that has to catch it.
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2],[]]}`))
	}))
	defer srv.Close()

	_, err := NewCohereProvider("k", srv.URL, "m", 2).EmbedBatch(context.Background(), []string{"a", "b"}, RoleDocument)
	if err == nil {
		t.Fatal("an empty vector from Cohere was returned as a success")
	}
	if !strings.Contains(err.Error(), "returned no embedding for text 2") {
		t.Errorf("error = %q — it failed, but not on the empty vector", err)
	}
}
