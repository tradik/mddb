package main

import (
	"fmt"
	"hash/crc32"
	"testing"
)

// The checksum reflects the document set (GO-050 follow-up): two nodes that
// hold different documents report different checksums. The old XOR of CRC32
// values gave a node missing its last 1,000 documents the checksum of the
// node that had them.

// emptyChecksum is the checksum of a collection without documents.
const emptyChecksum = "0000000000000000"

func TestChecksumsOfDifferentDocumentSetsDiffer(t *testing.T) {
	full, cleanupFull := newHandlerTestServer(t)
	defer cleanupFull()
	short, cleanupShort := newHandlerTestServer(t)
	defer cleanupShort()
	// The harness case: the same first documents, then 1,000 more on one
	// node only, keys differing in a counter.
	for i := range 1200 {
		key := fmt.Sprintf("late-%08d", i)
		addTestDoc(t, full, "repl", key, "en", fmt.Sprintf("document %d", i), nil)
		if i < 200 {
			addTestDoc(t, short, "repl", key, "en", fmt.Sprintf("document %d", i), nil)
		}
	}
	a, n := full.collectionChecksum("repl")
	b, m := short.collectionChecksum("repl")
	if n != 1200 || m != 200 || a == b {
		t.Fatalf("1200 documents: %s; 200 of them: %s", a, b)
	}
	// The stats walk reports the same figure.
	if snap, err := full.walkStats(); err != nil || snap.Collections["repl"].Checksum != a {
		t.Errorf("stats checksum %v, /v1/checksum %s", snap.Collections["repl"], a)
	}
}

func TestChecksumFollowsKeysLanguagesAndContent(t *testing.T) {
	base := func() *Server {
		s, cleanup := newHandlerTestServer(t)
		t.Cleanup(cleanup)
		addTestDoc(t, s, "blog", "a", "en", "alpha", nil)
		addTestDoc(t, s, "blog", "b", "en", "beta", nil)
		return s
	}
	changes := map[string]func(s *Server){
		"another language": func(s *Server) { addTestDoc(t, s, "blog", "a", "pl", "alpha", nil) },
		"changed content":  func(s *Server) { addTestDoc(t, s, "blog", "b", "en", "beta, edited", nil) },
		"a removed document": func(s *Server) {
			if err := s.deleteDocumentInternal("blog", "a", "en"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range changes {
		s := base()
		before, _ := s.collectionChecksum("blog")
		change(s)
		if after, _ := s.collectionChecksum("blog"); after == before {
			t.Errorf("%s left the checksum at %s", name, before)
		}
	}
}

// The exact shape the benchmark harness hit: 1,000 extra documents whose
// keys differ only in a counter and whose stored values start alike.
func TestAThousandCounterKeysDoNotCancelOut(t *testing.T) {
	value := []byte(`{"content":"same opening bytes for every one of these documents ......."}`)
	var before, after docSetSum
	var oldBefore, oldAfter uint32
	add := func(sum *docSetSum, old *uint32, key []byte) {
		sum.add(key, value)
		*old ^= crc32.ChecksumIEEE(key) ^ crc32.ChecksumIEEE(value[:64]) // the checksum before 2.19.8
	}
	for i := range 200 {
		key := []byte(fmt.Sprintf("doc|repl|repl|base-%08d|en", i))
		add(&before, &oldBefore, key)
		add(&after, &oldAfter, key)
	}
	for i := range 1000 {
		add(&after, &oldAfter, []byte(fmt.Sprintf("doc|repl|repl|late-%08d|en", i)))
	}
	if oldBefore != oldAfter {
		t.Fatal("the old checksum no longer collides here; the test does not show the defect")
	}
	if before == after {
		t.Errorf("1,000 more documents left the checksum at %s", before)
	}
}

func TestChecksumIsIndependentOfOrder(t *testing.T) {
	var a, b docSetSum
	a.add([]byte("doc|c|1"), []byte("x"))
	a.add([]byte("doc|c|2"), []byte("y"))
	b.add([]byte("doc|c|2"), []byte("y"))
	b.add([]byte("doc|c|1"), []byte("x"))
	if a != b || a.String() == emptyChecksum || len(a.String()) != 16 {
		t.Errorf("order changed the checksum: %s / %s", a, b)
	}
	var c docSetSum
	c.add([]byte("doc|c|1"), []byte("y")) // a value moved to another key
	c.add([]byte("doc|c|2"), []byte("x"))
	if c == a {
		t.Error("swapping values between documents went unnoticed")
	}
}
