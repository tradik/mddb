package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"

	"github.com/klauspost/compress/zstd"
)

// AGT-005: the archive refuses anything but manifest.json then collection.db,
// and a collection.db whose checksum is not the manifest's.

type tarMember struct {
	name string
	typ  byte
	body []byte
	size int64 // when not len(body): a header that lies
}

// tarZst packs members as a pack is packed.
func tarZst(t *testing.T, members ...tarMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, m := range members {
		size := m.size
		if size == 0 {
			size = int64(len(m.body))
		}
		typ := m.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Typeflag: typ, Size: size, Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(m.body)
	}
	// A header that lies about its size leaves the writer short; the stream
	// is cut there, as a truncated download is.
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// manifestFor is a valid manifest for data.
func manifestFor(t *testing.T, data []byte) []byte {
	t.Helper()
	m := validManifest()
	sum := sha256.Sum256(data)
	m.SHA256, m.Bytes = hex.EncodeToString(sum[:]), int64(len(data))
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func emptyRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestReadCorpusArchiveRefusals(t *testing.T) {
	data := []byte("collection bytes")
	manifest := manifestFor(t, data)
	bad := validManifest()
	bad.License = ""
	badManifest, _ := json.Marshal(bad)
	cases := []struct {
		name    string
		archive []byte
		max     int64
		want    string
	}{
		{"not zstd", []byte("plain text"), 1 << 20, "is it a .mddbpack"},
		{"data first", tarZst(t, tarMember{name: corpusDataName, body: data}), 1 << 20, "expected manifest.json"},
		{"a directory", tarZst(t, tarMember{name: corpusManifestName, typ: tar.TypeDir}), 1 << 20, "expected manifest.json"},
		{"manifest not JSON", tarZst(t, tarMember{name: corpusManifestName, body: []byte("{")}), 1 << 20, "manifest.json"},
		{"manifest without licence", tarZst(t, tarMember{name: corpusManifestName, body: badManifest}), 1 << 20, "needs a licence"},
		{"no data", tarZst(t, tarMember{name: corpusManifestName, body: manifest}), 1 << 20, "reading the archive"},
		{"data too large", tarZst(t, tarMember{name: corpusManifestName, body: manifest},
			tarMember{name: corpusDataName, body: data}), 4, "over the limit"},
		{"data cut short", tarZst(t, tarMember{name: corpusManifestName, body: manifest},
			tarMember{name: corpusDataName, body: data[:4], size: int64(len(data))}), 1 << 20, "collection.db"},
		{"wrong checksum", tarZst(t, tarMember{name: corpusManifestName, body: manifest},
			tarMember{name: corpusDataName, body: []byte("other bytes, same size")}), 1 << 20, "damaged or was altered"},
		{"a third member", tarZst(t, tarMember{name: corpusManifestName, body: manifest},
			tarMember{name: corpusDataName, body: data}, tarMember{name: "extra", body: data}), 1 << 20, "more than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readCorpusArchive(bytes.NewReader(tc.archive), emptyRoot(t), tc.max)
			if err == nil || !errors.Is(err, errCorpusInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want an invalid-pack error saying %q", err, tc.want)
			}
		})
	}
}

func TestReadCorpusArchiveRefusesToOverwrite(t *testing.T) {
	data := []byte("collection bytes")
	root := emptyRoot(t)
	if err := root.WriteFile(corpusDataName, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := tarZst(t, tarMember{name: corpusManifestName, body: manifestFor(t, data)}, tarMember{name: corpusDataName, body: data})
	if _, err := readCorpusArchive(bytes.NewReader(archive), root, 1<<20); err == nil {
		t.Fatal("an existing collection.db was overwritten")
	}
}

func TestWriteCorpusMembersFailures(t *testing.T) {
	m := validManifest()
	if err := writeCorpusMembers(tar.NewWriter(&bytes.Buffer{}), m, emptyRoot(t)); err == nil {
		t.Fatal("a build without collection.db was written")
	}
	root := emptyRoot(t)
	_ = root.WriteFile(corpusDataName, []byte("x"), 0o600)
	tw := tar.NewWriter(&bytes.Buffer{})
	_ = tw.Close()
	if err := writeCorpusMembers(tw, m, root); err == nil {
		t.Fatal("a closed archive took the manifest")
	}
	if err := writeCorpusMember(tw, "x", 1, time.Now(), strings.NewReader("x")); err == nil {
		t.Fatal("a closed archive took a member")
	}
}
