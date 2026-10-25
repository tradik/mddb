package main

import (
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// applyCurationFTS mutates results in place: drops hidden docs and splices
// pinned docs into their requested 1-based positions. Pinned docs get
// `Pinned: true` on the wire so clients can style them.
func (s *Server) applyCurationFTS(collection, query string, in []FTSResultWithDoc) []FTSResultWithDoc {
	if s.CurationManager == nil {
		return in
	}
	rules := s.CurationManager.MatchingRules(collection, query)
	if len(rules) == 0 {
		return in
	}
	hides, pins := collectPinsAndHides(rules)
	if len(hides) == 0 && len(pins) == 0 {
		return in
	}

	pinnedKeys := make(map[string]bool, len(pins))
	for _, p := range pins {
		pinnedKeys[p.Key] = true
	}
	filtered := make([]FTSResultWithDoc, 0, len(in))
	for _, r := range in {
		if hides[r.Document.Key] {
			continue
		}
		if pinnedKeys[r.Document.Key] {
			continue
		}
		filtered = append(filtered, r)
	}

	loaded := s.loadPinnedFTS(collection, pins)
	return splicePinnedFTS(filtered, loaded)
}

// applyCurationHybrid mirrors applyCurationFTS for hybrid results and
// re-numbers Rank after splicing.
func (s *Server) applyCurationHybrid(collection, query string, in []HybridSearchResultItem) []HybridSearchResultItem {
	if s.CurationManager == nil {
		return in
	}
	rules := s.CurationManager.MatchingRules(collection, query)
	if len(rules) == 0 {
		return in
	}
	hides, pins := collectPinsAndHides(rules)
	if len(hides) == 0 && len(pins) == 0 {
		return in
	}

	pinnedKeys := make(map[string]bool, len(pins))
	for _, p := range pins {
		pinnedKeys[p.Key] = true
	}
	filtered := make([]HybridSearchResultItem, 0, len(in))
	for _, r := range in {
		if hides[r.Document.Key] {
			continue
		}
		if pinnedKeys[r.Document.Key] {
			continue
		}
		filtered = append(filtered, r)
	}

	loaded := s.loadPinnedHybrid(collection, pins)
	out := splicePinnedHybrid(filtered, loaded)
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// collectPinsAndHides merges rules into a dedup hides-set and an ordered pin
// slice. Pins sorted by ascending Position (1-based); pins with Position<=0
// are appended in key order. Later rules overwrite earlier pins for the same
// key — enables a "disable" rule to silently replace a pinning rule.
func collectPinsAndHides(rules []*CurationRule) (map[string]bool, []pinResolved) {
	hides := map[string]bool{}
	byKey := map[string]pinResolved{}
	for _, r := range rules {
		for _, h := range r.Hides {
			hides[h] = true
		}
		for _, p := range r.Pins {
			byKey[p.Key] = pinResolved(p)
		}
	}
	if len(byKey) == 0 {
		return hides, nil
	}
	list := make([]pinResolved, 0, len(byKey))
	for _, p := range byKey {
		list = append(list, p)
	}
	// Insertion sort — typical pin-count is single digits.
	for i := 1; i < len(list); i++ {
		cur := list[i]
		j := i - 1
		for j >= 0 && pinLess(cur, list[j]) {
			list[j+1] = list[j]
			j--
		}
		list[j+1] = cur
	}
	return hides, list
}

type pinResolved struct {
	Key      string
	Lang     string
	Position int
}

func pinLess(a, b pinResolved) bool {
	aTail := a.Position <= 0
	bTail := b.Position <= 0
	if aTail != bTail {
		return bTail // positive positions first
	}
	if aTail && bTail {
		return a.Key < b.Key
	}
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	return a.Key < b.Key
}

// pinnedFTS pairs a loaded doc with its requested position so the splicer
// can route it to the right slot.
type pinnedFTS struct {
	Result   FTSResultWithDoc
	Position int
}

type pinnedHybrid struct {
	Result   HybridSearchResultItem
	Position int
}

// loadPinnedFTS resolves each pin to a concrete storage.Doc and returns a slice
// aligned 1:1 with the input (but skipping docs that can't be found). A
// mistyped pin key is silently ignored so it doesn't break every search.
func (s *Server) loadPinnedFTS(collection string, pins []pinResolved) []pinnedFTS {
	if len(pins) == 0 {
		return nil
	}
	out := make([]pinnedFTS, 0, len(pins))
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bByK := tx.Bucket([]byte("bykey"))
		for _, p := range pins {
			doc, ok := resolvePinnedDoc(bDocs, bByK, collection, p)
			if !ok {
				continue
			}
			out = append(out, pinnedFTS{
				Result: FTSResultWithDoc{
					Document: doc,
					Score:    0,
					Pinned:   true,
				},
				Position: p.Position,
			})
		}
		return nil
	})
	return out
}

func (s *Server) loadPinnedHybrid(collection string, pins []pinResolved) []pinnedHybrid {
	if len(pins) == 0 {
		return nil
	}
	out := make([]pinnedHybrid, 0, len(pins))
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bByK := tx.Bucket([]byte("bykey"))
		for _, p := range pins {
			doc, ok := resolvePinnedDoc(bDocs, bByK, collection, p)
			if !ok {
				continue
			}
			out = append(out, pinnedHybrid{
				Result: HybridSearchResultItem{
					Document: doc,
					Pinned:   true,
				},
				Position: p.Position,
			})
		}
		return nil
	})
	return out
}

// resolvePinnedDoc looks up a document by key (+ optional lang). With no
// lang the first hit under the bykey prefix wins — acceptable for the
// single-language case where Key is already unique.
func resolvePinnedDoc(bDocs, bByK *bolt.Bucket, collection string, p pinResolved) (storage.Doc, bool) {
	var docID string
	if p.Lang != "" {
		v := bByK.Get(storage.ByKeyKey(collection, p.Key, p.Lang))
		if v == nil {
			return storage.Doc{}, false
		}
		docID = string(v)
	} else {
		prefix := []byte("bykey|" + collection + "|" + p.Key + "|")
		c := bByK.Cursor()
		for k, v := c.Seek(prefix); k != nil && hasBytePrefix(k, prefix); k, v = c.Next() {
			docID = string(v)
			break
		}
		if docID == "" {
			return storage.Doc{}, false
		}
	}
	v := bDocs.Get(storage.DocKey(collection, docID))
	if v == nil {
		return storage.Doc{}, false
	}
	docPtr, err := loadDoc(v)
	if err != nil {
		return storage.Doc{}, false
	}
	if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < currentUnix() {
		return storage.Doc{}, false
	}
	return *docPtr, true
}

func hasBytePrefix(s, prefix []byte) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i, b := range prefix {
		if s[i] != b {
			return false
		}
	}
	return true
}

// splicePinnedFTS merges base (organic) with pinned results. Positive
// Positions place pins at that 1-based slot; clamped to the end if out of
// range. Position<=0 pins are appended after all organic results.
func splicePinnedFTS(base []FTSResultWithDoc, pinned []pinnedFTS) []FTSResultWithDoc {
	if len(pinned) == 0 {
		return base
	}
	out := make([]FTSResultWithDoc, 0, len(base)+len(pinned))
	bi := 0
	for _, p := range pinned {
		if p.Position <= 0 {
			continue
		}
		for len(out) < p.Position-1 && bi < len(base) {
			out = append(out, base[bi])
			bi++
		}
		out = append(out, p.Result)
	}
	for ; bi < len(base); bi++ {
		out = append(out, base[bi])
	}
	for _, p := range pinned {
		if p.Position <= 0 {
			out = append(out, p.Result)
		}
	}
	return out
}

func splicePinnedHybrid(base []HybridSearchResultItem, pinned []pinnedHybrid) []HybridSearchResultItem {
	if len(pinned) == 0 {
		return base
	}
	out := make([]HybridSearchResultItem, 0, len(base)+len(pinned))
	bi := 0
	for _, p := range pinned {
		if p.Position <= 0 {
			continue
		}
		for len(out) < p.Position-1 && bi < len(base) {
			out = append(out, base[bi])
			bi++
		}
		out = append(out, p.Result)
	}
	for ; bi < len(base); bi++ {
		out = append(out, base[bi])
	}
	for _, p := range pinned {
		if p.Position <= 0 {
			out = append(out, p.Result)
		}
	}
	return out
}
