package fts

import (
	"fmt"
	"strings"
	"testing"
)

// The write cost of a metadata-only update (#367).
//
// The issue's database is 111k documents on spinning disks, where an update
// costs what it writes: every key is a leaf page that may have to be read
// first. These tests measure what one update writes, three ways: the keys
// whose stored bytes change, the document's whole FTS footprint (what a full
// re-index deletes and writes again), and the pages bbolt writes at commit.

// costCorpus is a collection like the issue's: documents of a few hundred
// words with ten meta keys each.
func costCorpus(n int) []BulkDoc {
	words := strings.Fields("port package build ports freebsd library server daemon network kernel driver " +
		"compiler linker python perl ruby shell editor terminal graphics audio video codec archive backup " +
		"security crypto ssl ssh mail news chat irc database cache proxy firewall router monitor logging")
	docs := make([]BulkDoc, n)
	for i := range docs {
		var body strings.Builder
		for w := 0; w < 300; w++ {
			body.WriteString(words[(i*7+w*13)%len(words)])
			fmt.Fprintf(&body, " v%d ", (i+w)%97)
		}
		fields := map[string]string{"content": body.String()}
		for m := 0; m < 10; m++ {
			fields[fmt.Sprintf("meta.key%d", m)] = fmt.Sprintf("%s value%d", words[(i+m)%len(words)], (i*m)%50)
		}
		docs[i] = BulkDoc{DocID: fmt.Sprintf("port-%05d", i), Content: body.String(), Lang: "en", Fields: fields}
	}
	return docs
}

// updateCost is what one IndexDocs call wrote.
type updateCost struct {
	changedKeys   int   // keys added, changed or removed
	footprintKeys int   // keys of the document before the update
	pagesWritten  int64 // pages bbolt wrote at commit
	nodes         int64 // B+tree nodes materialised for writing
}

func measureUpdate(t testing.TB, idx *FTSIndex, doc BulkDoc) updateCost {
	t.Helper()
	before := dumpBuckets(t, idx)
	footprint := 0
	for k := range before {
		if strings.Contains(k, "|"+doc.DocID) {
			footprint++
		}
	}
	stats := idx.db.Stats()
	if err := idx.IndexDocs("ports", []BulkDoc{doc}); err != nil {
		t.Fatal(err)
	}
	now := idx.db.Stats()
	diff := now.TxStats.Sub(&stats.TxStats)
	after := dumpBuckets(t, idx)
	changed := 0
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			changed++
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			changed++
		}
	}
	return updateCost{changed, footprint, diff.GetWrite(), diff.GetNodeCount()}
}

func TestMetaOnlyUpdateWriteCost(t *testing.T) {
	idx := newBulkTestIndex(t)
	docs := costCorpus(2000)
	for start := 0; start < len(docs); start += 500 {
		if err := idx.IndexDocs("ports", docs[start:start+500]); err != nil {
			t.Fatal(err)
		}
	}
	// Before #367 every update was a full re-index: Rebuild does exactly
	// that, so it is the baseline.
	doc := docs[1000]
	doc.Fields = cloneFields(doc.Fields)
	doc.Fields["meta.license"] = "MIT"
	doc.Rebuild = true
	full := measureUpdate(t, idx, doc)
	t.Logf("full re-index (before #367): %d keys deleted and rewritten, %d pages written, %d nodes",
		full.footprintKeys, full.pagesWritten, full.nodes)

	doc = docs[1001]
	doc.Fields = cloneFields(doc.Fields)
	doc.Fields["meta.license"] = "MIT"
	inc := measureUpdate(t, idx, doc)
	t.Logf("meta-only update (add license: MIT): %d keys written, %d pages written, %d nodes",
		inc.changedKeys, inc.pagesWritten, inc.nodes)

	same := measureUpdate(t, idx, doc)
	t.Logf("identical resend: %d keys written, %d pages written, %d nodes", same.changedKeys, same.pagesWritten, same.nodes)

	// The new posting, the field's length and statistics, the field
	// reverse index and the signature.
	if inc.changedKeys > 5 || inc.pagesWritten*10 > full.pagesWritten {
		t.Errorf("a metadata-only update cost %+v against %+v for a full re-index", inc, full)
	}
	if same.changedKeys != 0 || same.pagesWritten != 0 {
		t.Errorf("an identical resend wrote %+v", same)
	}
}

func cloneFields(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
