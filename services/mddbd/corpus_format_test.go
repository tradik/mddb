package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/migrate"
	"mddb/proto"
)

// FE-022: what a format-2 manifest and archive must say before anything of
// a pack of several collections is installed.

// multiManifest is a valid format-2 manifest of collections a and b holding
// data, whose checksums match.
func multiManifest(data []byte) *corpusManifest {
	sum := sha256.Sum256(data)
	part := func(name string) corpusPart {
		return corpusPart{Name: name, License: "MIT", SchemaVersion: migrate.Latest(), DocCount: 1,
			SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}
	}
	return &corpusManifest{Format: corpusFormatMulti, Name: "kb", License: "MIT", MddbVersion: VERSION,
		Collections: []corpusPart{part("a"), part("b")}}
}

func TestMultiManifestValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(m *corpusManifest)
		want   string
	}{
		{"valid", func(*corpusManifest) {}, ""},
		{"no collections", func(m *corpusManifest) { m.Collections = nil }, "1 to 64"},
		{"too many", func(m *corpusManifest) { m.Collections = make([]corpusPart, corpusMaxCollections+1) }, "1 to 64"},
		{"twice", func(m *corpusManifest) { m.Collections[1].Name = "a" }, "named twice"},
		{"bad name", func(m *corpusManifest) { m.Collections[1].Name = "../b" }, "invalid pack name"},
		{"no licence", func(m *corpusManifest) { m.Collections[1].License = "" }, `"b" has no licence`},
		{"newer format", func(m *corpusManifest) { m.Collections[1].SchemaVersion = migrate.Latest() + 1 }, "upgrade it first"},
		{"no sha256", func(m *corpusManifest) { m.Collections[1].SHA256 = "" }, "sha256 of collection-2.db"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := multiManifest(nil)
			tc.change(m)
			err := m.validate()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadMultiCorpusArchiveRefusals(t *testing.T) {
	data := []byte("collection bytes")
	raw, _ := json.Marshal(multiManifest(data))
	manifest := tarMember{name: corpusManifestName, body: raw}
	one := tarMember{name: "collection-1.db", body: data}
	cases := []struct {
		name    string
		members []tarMember
		max     int64
		want    string
	}{
		{"valid", []tarMember{manifest, one, {name: "collection-2.db", body: data}}, 1 << 20, ""},
		{"a name from the manifest's format 1", []tarMember{manifest, {name: corpusDataName, body: data}}, 1 << 20, "expected collection-1.db"},
		{"damaged second file", []tarMember{manifest, one, {name: "collection-2.db", body: []byte("collection bytez")}}, 1 << 20, "collection-2.db has sha256"},
		{"over the limit together", []tarMember{manifest, one, {name: "collection-2.db", body: data}}, int64(len(data)) + 4, "over the limit"},
		{"a file too many", []tarMember{manifest, one, {name: "collection-2.db", body: data}, {name: "collection-3.db", body: data}}, 1 << 20, "more than its manifest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := readCorpusArchive(bytes.NewReader(tarZst(t, tc.members...)), emptyRoot(t), tc.max)
			if tc.want == "" {
				if err != nil || len(m.parts()) != 2 {
					t.Fatalf("a valid archive: %v", err)
				}
				return
			}
			if !errors.Is(err, errCorpusInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCorpusMemberNames(t *testing.T) {
	if corpusMemberName(corpusFormat, 0) != corpusDataName || corpusMemberName(corpusFormatMulti, 1) != "collection-2.db" {
		t.Fatal("member names changed")
	}
	m := multiManifest([]byte("x"))
	m.setParts(m.Collections)
	if m.DocCount != 2 || m.Bytes != 2 || m.SchemaVersion != migrate.Latest() {
		t.Fatalf("totals %+v", m)
	}
}

func TestCorpusBuildFromProtoCollections(t *testing.T) {
	req := corpusBuildFromProto(&proto.BuildCorpusPackRequest{Name: "kb", License: "MIT",
		Sources: []*proto.CorpusSourceProto{{Repo: "r", License: "MIT"}},
		Collections: []*proto.CorpusCollectionProto{{Name: "a"}, {Name: "b", License: "Apache-2.0",
			Sources: []*proto.CorpusSourceProto{{Repo: "s", Commit: "c", License: "Apache-2.0"}}}}})
	if req.Name != "kb" || len(req.Collections) != 2 || req.Collections[1].Sources[0].Commit != "c" || len(req.Sources) != 1 {
		t.Fatalf("request %+v", req)
	}
	info := corpusInfoToProto(&CorpusInfo{Name: "kb", PackLicense: "MIT", Collections: []string{"a", "b"},
		Description: "d", Bytes: 9, Sources: []corpusSource{{Repo: "r", License: "MIT"}}})
	if info.GetPackLicense() != "MIT" || len(info.GetCollections()) != 2 || info.GetSources()[0].GetRepo() != "r" ||
		info.GetDescription() != "d" || info.GetBytes() != 9 {
		t.Fatalf("info %+v", info)
	}
}
