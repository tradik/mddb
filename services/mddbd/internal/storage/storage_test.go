package storage

import "testing"

func TestDocProtoRoundTrip(t *testing.T) {
	orig := &Doc{
		ID:        "id1",
		Key:       "homepage",
		Lang:      "en_GB",
		Meta:      map[string][]string{"author": {"alice", "bob"}, "tag": {"x"}},
		ContentMD: "# Hello",
		AddedAt:   100,
		UpdatedAt: 200,
		ExpiresAt: 300,
	}
	got := ProtoToDoc(DocToProto(orig))
	if got.ID != orig.ID || got.Key != orig.Key || got.Lang != orig.Lang ||
		got.ContentMD != orig.ContentMD || got.AddedAt != orig.AddedAt ||
		got.UpdatedAt != orig.UpdatedAt || got.ExpiresAt != orig.ExpiresAt {
		t.Fatalf("round-trip scalar mismatch: %+v", got)
	}
	if len(got.Meta["author"]) != 2 || got.Meta["author"][0] != "alice" || got.Meta["tag"][0] != "x" {
		t.Fatalf("round-trip meta mismatch: %+v", got.Meta)
	}
	// Empty doc round-trips without panicking.
	if e := ProtoToDoc(DocToProto(&Doc{})); e == nil {
		t.Fatal("empty doc round-trip returned nil")
	}
}

func TestKeyBuilders(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"DocKey", DocKey("blog", "d1"), "doc|blog|d1"},
		{"ByKeyKey", ByKeyKey("blog", "home", "en"), "bykey|blog|home|en"},
		// DOC-017: key and language as the identifier has them; the
		// collection, which is case-sensitive, as written.
		{"ByKeyKey normalises", ByKeyKey("Blog", "Home/README.md", "EN"), "bykey|Blog|home/readme.md|en"},
		{"AppendByKeyKey", AppendByKeyKey([]byte("reused"), "c", "K", "L"), "reusedbykey|c|k|l"},
		{"ByKeyCollectionPrefix", ByKeyCollectionPrefix("Blog"), "bykey|Blog|"},
		{"ByKeyPathPrefix", ByKeyPathPrefix("Blog", "Docs/"), "bykey|Blog|docs/"},
		{"RevPrefix", RevPrefix("blog", "d1"), "rev|blog|d1|"},
		{"MetaKeyPrefix", MetaKeyPrefix("blog", "author", "alice"), "meta|blog|author|alice|"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
