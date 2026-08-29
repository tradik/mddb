package vector

import (
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

// provenanceBucket holds one record per collection describing how that
// collection's vectors were produced.
//
// It is a bucket of its own rather than a "prov|" prefix inside the vectors
// bucket because several places scan the vectors bucket key by key; a record
// that is not a vector has no business appearing in those scans.
var provenanceBucket = []byte("vector_provenance")

// Provenance records how a collection's vectors were produced.
//
// Variant is the part that can change without the model name changing. When a
// provider starts sending a task prefix or an input_type it has always sent
// the same model name, so Model alone cannot tell an old vector from a new
// one; Variant can. An empty Variant means "the way MDDB has always embedded
// documents with this provider", which is exactly what every collection
// written before this record existed contains.
type Provenance struct {
	Model     string `json:"model"`
	Variant   string `json:"variant,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// NoteProvenance records how the collection is being embedded right now.
//
// It is called on every write, so the common case — the provenance is already
// what it should be — must not cost a transaction. The in-memory map answers
// that case; BoltDB is touched only when the collection is new to this process
// or its embedding actually changed.
func (vs *VectorStore) NoteProvenance(collection, model, variant string) error {
	want := Provenance{Model: model, Variant: variant}

	vs.provMu.RLock()
	have, seen := vs.prov[collection]
	vs.provMu.RUnlock()
	if seen && have.Model == want.Model && have.Variant == want.Variant {
		return nil
	}

	vs.provMu.Lock()
	defer vs.provMu.Unlock()
	// Re-check: another writer may have persisted it while we waited.
	if have, seen := vs.prov[collection]; seen && have.Model == want.Model && have.Variant == want.Variant {
		return nil
	}

	want.UpdatedAt = time.Now().Unix()
	encoded, err := json.Marshal(want)
	if err != nil {
		return err
	}
	err = vs.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(provenanceBucket)
		if err != nil {
			return err
		}
		return b.Put([]byte(collection), encoded)
	})
	if err != nil {
		return err
	}
	if vs.prov == nil {
		vs.prov = make(map[string]Provenance)
	}
	vs.prov[collection] = want
	return nil
}

// Provenance returns the recorded provenance for a collection. The second
// result is false when nothing was ever recorded, which is not an error: it
// means the collection predates this record and therefore carries the empty
// variant.
func (vs *VectorStore) Provenance(collection string) (Provenance, bool, error) {
	var (
		p     Provenance
		found bool
	)
	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(provenanceBucket)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(collection))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &p)
	})
	if err != nil {
		return Provenance{}, false, err
	}
	return p, found, nil
}

// StaleCollections lists the collections whose stored vectors were not
// produced the way the given model and variant would produce them now, and so
// would have to be reindexed for their vectors to be comparable with a query
// embedded today.
//
// A collection with no provenance record counts as the empty variant rather
// than being skipped. Skipping it would hide precisely the collections this
// exists to find: every collection written before provenance was recorded.
func (vs *VectorStore) StaleCollections(model, variant string) ([]string, error) {
	recorded, err := vs.allProvenance()
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, collection := range vs.collectionsWithVectors() {
		have := recorded[collection] // zero value == legacy: no model, empty variant
		if have.Variant == variant && (have.Model == "" || have.Model == model) {
			continue
		}
		stale = append(stale, collection)
	}
	return stale, nil
}

// allProvenance reads every recorded provenance, keyed by collection.
func (vs *VectorStore) allProvenance() (map[string]Provenance, error) {
	out := make(map[string]Provenance)
	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(provenanceBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var p Provenance
			if err := json.Unmarshal(v, &p); err != nil {
				return err
			}
			out[string(k)] = p
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// collectionsWithVectors lists the collections that actually hold vectors, in
// key order. Collections are read from the vectors themselves rather than from
// the provenance bucket so that a collection with no record still appears.
func (vs *VectorStore) collectionsWithVectors() []string {
	var (
		seen = make(map[string]bool)
		out  []string
	)
	_ = vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			parts := SplitKey(k)
			if len(parts) < 3 || parts[0] != "vec" || seen[parts[1]] {
				return nil
			}
			seen[parts[1]] = true
			out = append(out, parts[1])
			return nil
		})
	})
	return out
}
