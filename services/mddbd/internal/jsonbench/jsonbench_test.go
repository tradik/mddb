// GO-026: does the Go 1.27 standard library, now backed by the v2 engine,
// match goccy/go-json on the shapes MDDB actually serialises? goccy is
// imported by 104 files; dropping it would remove a dependency, shrink the
// binary and leave one JSON engine instead of two — but only if the numbers
// say the stdlib has caught up.
//
// The payloads are the real ones: a storage.Doc with multi-valued meta and a
// markdown body, an FTS result page with highlights, and a batch of 100 docs
// (the shape of an import response).
//
// Run: go test ./internal/jsonbench/ -bench . -benchmem -run '^$'
package jsonbench

import (
	stdjson "encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	goccy "github.com/goccy/go-json"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// --- payload builders -------------------------------------------------------

func makeDoc(bodyKB int) storage.Doc {
	return storage.Doc{
		ID:   "01J9ZK4M8N7P6Q5R4S3T2U1V0W",
		Key:  "guides/getting-started",
		Lang: "en_GB",
		Meta: map[string][]string{
			"tags":     {"guide", "getting-started", "docs", "beginner"},
			"category": {"documentation"},
			"authors":  {"ada", "grace"},
			"status":   {"published"},
		},
		ContentMD: strings.Repeat("# Heading\n\nSome **markdown** body text with a [link](/x).\n\n", bodyKB*12),
		AddedAt:   1755792000,
		UpdatedAt: 1755878400,
	}
}

type searchResult struct {
	Key        string              `json:"key"`
	Score      float64             `json:"score"`
	Highlights []fts.Highlight     `json:"highlights,omitempty"`
	Meta       map[string][]string `json:"meta,omitempty"`
}

func makeResults(n int) []searchResult {
	out := make([]searchResult, n)
	for i := range out {
		out[i] = searchResult{
			Key:   fmt.Sprintf("guides/page-%03d", i),
			Score: 0.8123 - float64(i)*0.001,
			Highlights: []fts.Highlight{
				{Fragment: "the <mark>quick</mark> brown fox jumps", MatchedTerms: []string{"quick"}, StartOffset: 120, EndOffset: 158},
				{Fragment: "a second <mark>quick</mark> fragment here", MatchedTerms: []string{"quick"}, StartOffset: 402, EndOffset: 441},
			},
			Meta: map[string][]string{"tags": {"guide", "docs"}, "status": {"published"}},
		}
	}
	return out
}

func makeBatch(n, bodyKB int) []storage.Doc {
	out := make([]storage.Doc, n)
	for i := range out {
		d := makeDoc(bodyKB)
		d.Key = fmt.Sprintf("guides/page-%03d", i)
		out[i] = d
	}
	return out
}

// --- the three engines ------------------------------------------------------

type engine struct {
	name      string
	marshal   func(any) ([]byte, error)
	unmarshal func([]byte, any) error
}

var engines = []engine{
	{"stdlib", stdjson.Marshal, stdjson.Unmarshal},
	{"goccy", goccy.Marshal, goccy.Unmarshal},
	{"jsonv2", func(v any) ([]byte, error) { return jsonv2.Marshal(v) },
		func(b []byte, v any) error { return jsonv2.Unmarshal(b, v) }},
}

// --- benchmarks -------------------------------------------------------------

func benchMarshal(b *testing.B, payload any) {
	for _, e := range engines {
		b.Run(e.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := e.marshal(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchUnmarshal[T any](b *testing.B, payload any) {
	data, err := stdjson.Marshal(payload)
	if err != nil {
		b.Fatal(err)
	}
	for _, e := range engines {
		b.Run(e.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var out T
				if err := e.unmarshal(data, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMarshalDocSmall(b *testing.B) { benchMarshal(b, makeDoc(1)) }
func BenchmarkMarshalDocLarge(b *testing.B) { benchMarshal(b, makeDoc(50)) }
func BenchmarkMarshalResults(b *testing.B)  { benchMarshal(b, makeResults(20)) }
func BenchmarkMarshalBatch100(b *testing.B) { benchMarshal(b, makeBatch(100, 1)) }

func BenchmarkUnmarshalDocSmall(b *testing.B) { benchUnmarshal[storage.Doc](b, makeDoc(1)) }
func BenchmarkUnmarshalDocLarge(b *testing.B) { benchUnmarshal[storage.Doc](b, makeDoc(50)) }
func BenchmarkUnmarshalResults(b *testing.B)  { benchUnmarshal[[]searchResult](b, makeResults(20)) }
func BenchmarkUnmarshalBatch100(b *testing.B) { benchUnmarshal[[]storage.Doc](b, makeBatch(100, 1)) }

// TestGoccyMatchesStdlibByteForByte is the precondition for the swap this
// benchmark exists to inform: goccy is only droppable if the stdlib produces
// the same bytes, because the output is persisted and replicated, not just
// returned over HTTP.
func TestGoccyMatchesStdlibByteForByte(t *testing.T) {
	for _, payload := range []any{makeDoc(2), makeResults(3), makeBatch(5, 1)} {
		want, err := stdjson.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		got, err := goccy.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("goccy and the stdlib disagree:\n stdlib: %s\n goccy:  %s", want, got)
		}
	}
}

// TestJSONV2DiffersOnTheWire records why the v2 API is benchmarked but not a
// migration candidate: it is a different encoder, not a faster one.
//
//   - v1 sorts map keys; v2 does not, so output stops being deterministic —
//     and MDDB persists documents and ships them through the replication
//     binlog, where identical input must give identical bytes.
//   - `omitempty` no longer drops a zero number (v2 spells that `omitzero`),
//     so Doc.ExpiresAt starts appearing as `"expiresAt":0`.
//
// The v1-compatible `encoding/json` path keeps both behaviours, which is the
// path a goccy migration would take.
func TestJSONV2DiffersOnTheWire(t *testing.T) {
	doc := makeDoc(0)
	doc.ContentMD = "x"

	v1, err := stdjson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := jsonv2.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(v1) == string(v2) {
		t.Skip("encoding/json/v2 now matches v1 byte-for-byte — re-evaluate the migration note")
	}
	if !strings.Contains(string(v2), `"expiresAt":0`) {
		t.Errorf("expected v2 to emit a zero expiresAt, got %s", v2)
	}
	if strings.Contains(string(v1), `"expiresAt"`) {
		t.Errorf("expected v1 to omit a zero expiresAt, got %s", v1)
	}

	// Both must still parse back through the v1 reader; the difference is in
	// what gets written, not in validity.
	var back storage.Doc
	if err := stdjson.Unmarshal(v2, &back); err != nil {
		t.Errorf("v2 output is not readable by v1: %v", err)
	}
	if back.Key != doc.Key {
		t.Error("v2 output did not round-trip")
	}
}
