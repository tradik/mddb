package main

import (
	"reflect"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestMetaValuesWithPrefix(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	content := "package svc\n"
	idx := "v1 " + semanticContentHash(content) + " 000000000000 gopls/0.24.0"
	for key, refs := range map[string][]string{
		"svc/a.go": {"svc/log.go#Log 1,2", "svc/log.go#Logf 3"},
		"svc/b.go": {"svc/log.go#Log 7"},
	} {
		meta := map[string][]string{MetaKeySymIndex: {idx}, MetaKeySymRefs: refs}
		if _, _, err := s.addDocumentIf(factsCollection, key, "en", meta, content, 0, false, ""); err != nil {
			t.Fatal(err)
		}
	}
	idA, idB := genID(factsCollection, "svc/a.go", "en"), genID(factsCollection, "svc/b.go", "en")
	if !strings.Contains(idA, "|") {
		t.Fatalf("document IDs are expected to contain |, got %q", idA)
	}
	// An entry this layout never writes: a value with no document ID after it.
	if err := s.DBUpdateC(factsCollection, func(tx *bolt.Tx) error {
		return tx.Bucket(s.BucketNames.IdxMeta).Put([]byte("meta|"+factsCollection+"|symRefs|svc/log.go#Log 9"), []byte("1"))
	}); err != nil {
		t.Fatal(err)
	}

	scan := func(prefix string, limit int) (hits []metaValueHit, more bool) {
		_ = s.DBViewC(factsCollection, func(tx *bolt.Tx) error {
			hits, more = metaValuesWithPrefix(tx.Bucket(s.BucketNames.IdxMeta), factsCollection, MetaKeySymRefs, prefix, limit)
			return nil
		})
		return hits, more
	}

	// The space ends the target, so Log does not also find Logf.
	hits, more := scan("svc/log.go#Log ", 10)
	want := []metaValueHit{{Value: "svc/log.go#Log 1,2", DocID: idA}, {Value: "svc/log.go#Log 7", DocID: idB}}
	if more || !reflect.DeepEqual(hits, want) {
		t.Errorf("hits = %+v (more %v), want %+v", hits, more, want)
	}
	if hits, more = scan("svc/log.go#Log ", 1); !more || len(hits) != 1 {
		t.Errorf("limit 1: %+v more=%v", hits, more)
	}
	if hits, _ = scan("svc/log.go#Logf", 10); len(hits) != 1 || hits[0].DocID != idA {
		t.Errorf("Logf: %+v", hits)
	}
	if hits, _ = scan("a|b", 10); hits != nil {
		t.Errorf("a prefix with | matched %+v", hits)
	}
	if hits, more = metaValuesWithPrefix(nil, factsCollection, MetaKeySymRefs, "x", 1); hits != nil || more {
		t.Error("a missing bucket should find nothing")
	}
}
