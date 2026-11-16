package geo

import "testing"

func TestApplyReplicatedKeepsBothIndexes(t *testing.T) {
	gi, gh := NewGeoIndex(), NewGeoHashIndex()
	key := buildGeoKey("c", "d1")
	ApplyReplicated(true, key, encodeGeoValue(52.23, 21.01), gi, gh)
	if gi.Len("c") != 1 || gh.Len("c") != 1 {
		t.Fatalf("after a put: %d and %d points", gi.Len("c"), gh.Len("c"))
	}
	// Unreadable keys and values change nothing.
	ApplyReplicated(true, []byte("nope"), encodeGeoValue(1, 1), gi, gh)
	ApplyReplicated(true, buildGeoKey("c", "d2"), []byte("short"), gi, gh)
	if gi.Len("c") != 1 || gh.Len("c") != 1 {
		t.Fatalf("an unreadable entry changed the indexes: %d and %d points", gi.Len("c"), gh.Len("c"))
	}
	ApplyReplicated(false, key, nil, gi, gh)
	if gi.Len("c") != 0 || gh.Len("c") != 0 {
		t.Fatalf("after a delete: %d and %d points", gi.Len("c"), gh.Len("c"))
	}
}
