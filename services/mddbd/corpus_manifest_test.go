package main

import (
	"strings"
	"testing"
	"time"

	"mddb/internal/migrate"
)

// AGT-005: what a manifest must say before a pack is built or installed.

func validManifest() *corpusManifest {
	return &corpusManifest{Format: corpusFormat, Name: "docs", License: "MIT", MddbVersion: VERSION,
		SchemaVersion: migrate.Latest(), SHA256: strings.Repeat("a", 64), DocCount: 1,
		Sources: []corpusSource{{Repo: "https://example.org/r", License: "MIT"}}}
}

func TestManifestValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(m *corpusManifest)
		want   string
	}{
		{"valid", func(*corpusManifest) {}, ""},
		{"format", func(m *corpusManifest) { m.Format = 2 }, "unsupported pack format"},
		{"name", func(m *corpusManifest) { m.Name = "../etc" }, "invalid pack name"},
		{"no licence", func(m *corpusManifest) { m.License = " " }, "needs a licence"},
		{"source without repo", func(m *corpusManifest) { m.Sources[0].Repo = "" }, "has no repo"},
		{"source without licence", func(m *corpusManifest) { m.Sources[0].License = "" }, "has no licence"},
		{"newer format", func(m *corpusManifest) { m.SchemaVersion = migrate.Latest() + 1 }, "upgrade it first"},
		{"no sha256", func(m *corpusManifest) { m.SHA256 = "" }, "no sha256"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.change(m)
			err := m.validate()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEmbeddingMismatch(t *testing.T) {
	m := validManifest()
	if why := m.embeddingMismatch("any", 3); why != "" {
		t.Fatalf("a full-text pack mismatches: %s", why)
	}
	m.Embedding = &corpusEmbedding{Model: "m1", Dims: 3}
	for _, tc := range []struct {
		model string
		dims  int
		want  string
	}{
		{"m1", 3, ""},
		{"", 0, "no embedding provider"},
		{"m1", 4, "3 dimensions"},
		{"m2", 3, "made with m1"},
	} {
		if why := m.embeddingMismatch(tc.model, tc.dims); tc.want == "" && why != "" || !strings.Contains(why, tc.want) {
			t.Fatalf("embeddingMismatch(%q, %d) = %q, want %q", tc.model, tc.dims, why, tc.want)
		}
	}
	info := m.info(true, time.Unix(0, 0))
	if !info.VectorsDisabled || info.Name != "docs" || info.Embedding == nil {
		t.Fatalf("info = %+v", info)
	}
}
