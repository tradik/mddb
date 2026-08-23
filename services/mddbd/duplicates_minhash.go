package main

import (
	"fmt"
	"sort"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/minhash"
	"mddb/internal/storage"
)

// Near-duplicate detection by text overlap (SRCH-002).
//
// The two existing modes answer different questions and both leave a gap.
// `exact` compares content hashes: one byte different and it sees nothing.
// `similar` compares embeddings, which measures topic — two independently
// written pages about certificate rotation score high without sharing a
// sentence, and a document with a paragraph appended scores about the same as
// one that merely mentions the subject.
//
// `minhash` compares the words themselves. A page forked and lightly edited
// scores near 1; a page on the same subject written from scratch scores near
// 0. It is also the only mode that works with no embedding provider
// configured, which is most installations.
//
// The implementation is shared with the diversity signal in search_fusion.go —
// one MinHash, two uses.

// minhashDoc is one document's signature.
type minhashDoc struct {
	docID     string
	signature minhash.Signature
}

// findMinHashDuplicates groups documents whose text overlaps above the
// threshold.
//
// Reads content rather than vectors, so it runs on collections that were never
// embedded. Union-Find groups transitively, matching what the other two modes
// do: if A overlaps B and B overlaps C, all three are one group even when A
// and C fall just under the threshold on their own.
func (s *Server) findMinHashDuplicates(collection string, threshold float64, includeContent bool) ([]DuplicateGroup, int, error) {
	if threshold <= 0 {
		// Text overlap runs higher than cosine similarity for unrelated
		// documents, so the default is stricter than the vector mode's.
		threshold = 0.7
	}

	docs, err := s.collectMinHashDocs(collection)
	if err != nil {
		return nil, 0, err
	}
	if len(docs) < 2 {
		return nil, len(docs), nil
	}

	uf := newUnionFind(len(docs))
	type pairScore struct {
		i, j  int
		score float64
	}
	var pairs []pairScore

	for i := 0; i < len(docs); i++ {
		for j := i + 1; j < len(docs); j++ {
			score := minhash.Similarity(docs[i].signature, docs[j].signature)
			if score >= threshold {
				uf.union(i, j)
				pairs = append(pairs, pairScore{i, j, score})
			}
		}
	}
	if len(pairs) == 0 {
		return nil, len(docs), nil
	}

	// Best score per member, so a group reports how alike its documents
	// actually are rather than just that they are alike.
	best := make(map[int]float64, len(docs))
	for _, p := range pairs {
		if p.score > best[p.i] {
			best[p.i] = p.score
		}
		if p.score > best[p.j] {
			best[p.j] = p.score
		}
	}

	members := make(map[int][]int)
	for i := range docs {
		root := uf.find(i)
		members[root] = append(members[root], i)
	}

	roots := make([]int, 0, len(members))
	for root, idx := range members {
		if len(idx) >= 2 {
			roots = append(roots, root)
		}
	}
	// Deterministic group numbering: map iteration order would renumber the
	// same duplicates differently on every call.
	sort.Ints(roots)

	groups := make([]DuplicateGroup, 0, len(roots))
	for n, root := range roots {
		idx := members[root]
		sort.Ints(idx)

		g := DuplicateGroup{GroupID: n + 1, Type: "minhash"}
		for _, i := range idx {
			info := DuplicateDocInfo{DocID: docs[i].docID, Score: float32(best[i])}
			if includeContent {
				if doc, err := s.LoadDocByID(collection, docs[i].docID); err == nil && doc != nil {
					info.ContentMD = doc.ContentMD
					info.Key = doc.Key
				}
			}
			g.Documents = append(g.Documents, info)
		}
		groups = append(groups, g)
	}
	return groups, len(docs), nil
}

// collectMinHashDocs reads every document in a collection and signs it.
//
// One pass over the bucket, signatures computed as documents are read: holding
// every body in memory to sign them afterwards would cost the whole corpus in
// RAM, and the signature is a few hundred bytes.
func (s *Server) collectMinHashDocs(collection string) ([]minhashDoc, error) {
	var docs []minhashDoc

	err := s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return nil
		}
		prefix := []byte("doc|" + collection + "|")
		cur := b.Cursor()
		for k, v := cur.Seek(prefix); k != nil && hasPrefixBytes(k, prefix); k, v = cur.Next() {
			doc, err := loadDoc(v)
			if err != nil || doc == nil || doc.ContentMD == "" {
				continue
			}
			sig := minhash.Compute(doc.ContentMD, minhash.DefaultShingleSize, minhash.DefaultSignatureSize)
			if sig == nil {
				continue
			}
			docs = append(docs, minhashDoc{docID: doc.ID, signature: sig})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", collection, err)
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].docID < docs[j].docID })
	return docs, nil
}

// LoadDocByID reads one document by its stored ID.
func (s *Server) LoadDocByID(collection, docID string) (*storage.Doc, error) {
	var doc *storage.Doc
	err := s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return nil
		}
		v := b.Get(storage.DocKey(collection, docID))
		if v == nil {
			return nil
		}
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc = d
		return nil
	})
	return doc, err
}

func hasPrefixBytes(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
