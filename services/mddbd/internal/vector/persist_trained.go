package vector

import (
	"fmt"
	"sync"
)

// The trained structures in a checkpoint (SRCH-023).
//
// IVF, PQ, OPQ, SQ and SQ4 each train something from a collection's vectors
// — centroids, codebooks, a rotation, per-dimension scales — and then file
// every vector under it. One section layout carries all five:
//
//	dim, trainedOn
//	matrices   what was trained, as rows of float32 (which ones: see each index)
//	codeWidth  bytes of code per vector: 2 for an IVF cluster number, one per
//	           subspace for PQ and OPQ, 0 for SQ and SQ4, whose codes are
//	           cheaper to compute than to read
//	entries    document, fingerprint of its vector, code
//
// At load an entry is used only for a stored vector with the same
// fingerprint; the others are filed the way a new vector is. If fewer than
// half of the stored vectors are found in the section, what was trained
// describes another corpus — the same threshold at which a collection that
// doubled is retrained — and the index trains again instead.

// persistMaxCodeWidth bounds the code bytes per entry.
const persistMaxCodeWidth = 1 << 12

// trainedSnapshot is a trained collection captured under the read lock.
// Matrices and vectors are slice headers onto memory that is replaced, never
// rewritten; ids and codes are copied.
type trainedSnapshot struct {
	dim, trainedOn int
	matrices       [][][]float32
	codeWidth      int
	ids            []string
	vecs           [][]float32
	codes          []byte
}

// add records one vector and its code.
func (s *trainedSnapshot) add(id string, v []float32, code ...byte) {
	s.ids = append(s.ids, id)
	s.vecs = append(s.vecs, v)
	s.codes = append(s.codes, code...)
}

// encode implements persistSnapshot.
func (s *trainedSnapshot) encode(e *persistEncoder) {
	e.u32(uint32(s.dim))           // #nosec G115 -- a vector length
	e.u64(uint64(s.trainedOn))     // #nosec G115 -- a count
	e.u32(uint32(len(s.matrices))) // #nosec G115 -- one per subspace and a rotation at most
	for _, m := range s.matrices {
		e.u32(uint32(len(m)))    // #nosec G115 -- rows of a trained structure
		e.u32(uint32(len(m[0]))) // #nosec G115 -- their length
		for _, row := range m {
			e.f32s(row)
		}
	}
	e.u32(uint32(s.codeWidth)) // #nosec G115 -- at most persistMaxCodeWidth
	e.u64(uint64(len(s.ids)))
	for i, id := range s.ids {
		e.str(id)
		e.u64(vectorFingerprint(s.vecs[i]))
		e.bytes(s.codes[i*s.codeWidth : (i+1)*s.codeWidth])
	}
}

// trainedSection is a trained section decoded from a checkpoint.
type trainedSection struct {
	dim, trainedOn int
	matrices       [][][]float32
	codeWidth      int
	ids            []string
	hashes         []uint64
	codes          []byte
}

// decodeTrainedSection reads a trained section. What the matrices mean is
// for the index to check; here they only have to fit in the file.
func decodeTrainedSection(d *persistDecoder) *trainedSection {
	s := &trainedSection{dim: int(d.u32())}
	trainedOn := d.u64()
	matrices := d.count(uint64(d.u32()), 8)
	if d.err == nil && (s.dim < 1 || s.dim > 1<<20 || trainedOn > 1<<40) {
		d.fail(corruptf("trained structure of dimension %d over %d vectors", s.dim, trainedOn))
	}
	s.trainedOn = int(trainedOn) // #nosec G115 -- bounded just above
	for i := 0; i < matrices && d.err == nil; i++ {
		s.matrices = append(s.matrices, decodeMatrix(d))
	}
	s.codeWidth = int(d.u32())
	if d.err == nil && s.codeWidth > persistMaxCodeWidth {
		d.fail(corruptf("codes of %d bytes", s.codeWidth))
	}
	// An entry is at least a key length, a fingerprint and its code.
	n := d.count(d.u64(), 12+s.codeWidth)
	for i := 0; i < n && d.err == nil; i++ {
		s.ids = append(s.ids, d.str())
		s.hashes = append(s.hashes, d.u64())
		s.codes = append(s.codes, d.fixed(s.codeWidth)...)
	}
	return s
}

// decodeMatrix reads rows of equal length.
func decodeMatrix(d *persistDecoder) [][]float32 {
	rows, cols := uint64(d.u32()), uint64(d.u32())
	if d.err == nil && (rows == 0 || cols == 0) {
		d.fail(corruptf("matrix of %d rows of %d", rows, cols))
	}
	d.count(rows*cols, 4)
	if d.err != nil {
		return nil
	}
	m := make([][]float32, rows)
	for i := range m {
		m[i] = d.f32s(int(cols)) // #nosec G115 -- rows*cols*4 fits in the file
	}
	return m
}

// match reports, for every entry, whether the store still holds the vector
// it was written for. It is ErrCheckpointStale when fewer than half of the
// stored vectors are covered.
func (s *trainedSection) match(vectors map[string][]float32) ([]bool, error) {
	ok := make([]bool, len(s.ids))
	matched := 0
	for i, id := range s.ids {
		if v, has := vectors[id]; has && len(v) == s.dim && vectorFingerprint(v) == s.hashes[i] {
			ok[i] = true
			matched++
		}
	}
	if 2*matched < len(vectors) {
		return nil, fmt.Errorf("%w: it covers %d of %d", ErrCheckpointStale, matched, len(vectors))
	}
	return ok, nil
}

// trainedSectionOf checks that a section is a trained one with the shape an
// index expects: how many matrices, and the code width.
func trainedSectionOf(section any, matrices, codeWidth int) (*trainedSection, error) {
	s, ok := section.(*trainedSection)
	if !ok || len(s.matrices) != matrices || s.codeWidth != codeWidth {
		return nil, corruptf("not the trained section this index writes")
	}
	return s, nil
}

// rowsOf checks that the first n matrices are single rows of dim values —
// per-dimension parameters — and returns those rows.
func (s *trainedSection) rowsOf(n int) ([][]float32, error) {
	rows := make([][]float32, n)
	for i := range rows {
		if len(s.matrices[i]) != 1 || len(s.matrices[i][0]) != s.dim {
			return nil, corruptf("parameter %d is not one value per dimension", i)
		}
		rows[i] = s.matrices[i][0]
	}
	return rows, nil
}

// restored is the train state of a collection loaded from a checkpoint.
func (s *trainedSection) restored() trainState {
	return trainState{trained: true, trainedOn: s.trainedOn, trainings: 1}
}

// stampUnder reads how often a collection has been trained, under the
// index's read lock; state returns nil for a collection the index never saw.
func stampUnder(mu *sync.RWMutex, state func() *trainState) uint64 {
	mu.RLock()
	defer mu.RUnlock()
	if t := state(); t != nil {
		return t.trainings
	}
	return 0
}

// --- IVF: one matrix, the centroids; the code is the cluster number ---

// PersistStamp implements Persistable.
func (idx *IVFIndex) PersistStamp(collection string) uint64 {
	return stampUnder(&idx.mu, func() *trainState {
		if c, ok := idx.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

func (idx *IVFIndex) persistKind() byte { return persistKindTrained }

// persistSnapshot implements Persistable.
func (idx *IVFIndex) persistSnapshot(collection string) persistSnapshot {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	c, ok := idx.data[collection]
	if !ok || !c.trained || len(c.centroids) == 0 {
		return nil
	}
	s := &trainedSnapshot{dim: len(c.centroids[0]), trainedOn: c.trainedOn,
		matrices: [][][]float32{c.centroids}, codeWidth: 2}
	for ci, cluster := range c.clusters {
		for id, v := range cluster {
			s.add(id, v, byte(ci), byte(ci>>8)) // #nosec G115 -- at most 256 clusters
		}
	}
	return s
}

// persistRestore implements Persistable.
func (idx *IVFIndex) persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error) {
	s, err := trainedSectionOf(section, 1, 2)
	if err != nil {
		return nil, err
	}
	centroids := s.matrices[0]
	if len(centroids[0]) != s.dim {
		return nil, corruptf("centroids of %d dimensions in a section of %d", len(centroids[0]), s.dim)
	}
	ok, err := s.match(vectors)
	if err != nil {
		return nil, err
	}
	c := &ivfCollection{centroids: centroids, clusters: make([]map[string][]float32, len(centroids)),
		allVecs: make(map[string][]float32, len(vectors)), trainState: s.restored()}
	for i := range c.clusters {
		c.clusters[i] = make(map[string][]float32)
	}
	for i, id := range s.ids {
		ci := int(s.codes[2*i]) | int(s.codes[2*i+1])<<8
		if ci >= len(centroids) {
			return nil, corruptf("cluster %d of %d", ci, len(centroids))
		}
		if ok[i] {
			c.clusters[ci][id] = vectors[id]
			c.allVecs[id] = vectors[id]
		}
	}
	idx.mu.Lock()
	idx.data[collection] = c
	idx.mu.Unlock()
	return missingFrom(vectors, c.allVecs), nil
}

// --- PQ and OPQ: one matrix per subspace, its codebook; OPQ puts its
// rotation first. The code is one codeword number per subspace ---

// codebooksOf checks codebooks against the dimension they split and the
// index's configuration, and every entry's code against the codebooks.
func (s *trainedSection) codebooksOf(books [][][]float32, nSubspaces, codebookSize int) error {
	nSub := min(nSubspaces, s.dim)
	if len(books) != nSub || s.codeWidth != nSub {
		return fmt.Errorf("%w: %d codebooks, the index is configured for %d", ErrCheckpointStale, len(books), nSub)
	}
	subDim := s.dim / nSub
	for i, book := range books {
		want := subDim
		if i == nSub-1 {
			want = s.dim - subDim*(nSub-1)
		}
		if len(book) > codebookSize || len(book[0]) != want {
			return corruptf("codebook %d has %d codewords of %d, want at most %d of %d", i, len(book), len(book[0]), codebookSize, want)
		}
	}
	for i, code := range s.codes {
		if int(code) >= len(books[i%nSub]) {
			return corruptf("codeword %d of %d", code, len(books[i%nSub]))
		}
	}
	return nil
}

// codesOf returns the codes and vectors of the entries the store still
// holds; each code is a slice of the section's own buffer.
func (s *trainedSection) codesOf(vectors map[string][]float32) (map[string][]uint8, map[string][]float32, error) {
	ok, err := s.match(vectors)
	if err != nil {
		return nil, nil, err
	}
	codes := make(map[string][]uint8, len(s.ids))
	vecs := make(map[string][]float32, len(vectors))
	w := s.codeWidth
	for i, id := range s.ids {
		if ok[i] {
			codes[id] = s.codes[i*w : (i+1)*w : (i+1)*w]
			vecs[id] = vectors[id]
		}
	}
	return codes, vecs, nil
}

// snapshotCodes captures every coded vector of a PQ-style collection.
func snapshotCodes(s *trainedSnapshot, codes map[string][]uint8, vecs map[string][]float32) *trainedSnapshot {
	for id, code := range codes {
		s.add(id, vecs[id], code...)
	}
	return s
}

// PersistStamp implements Persistable.
func (p *PQIndex) PersistStamp(collection string) uint64 {
	return stampUnder(&p.mu, func() *trainState {
		if c, ok := p.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

func (p *PQIndex) persistKind() byte { return persistKindTrained }

// persistSnapshot implements Persistable.
func (p *PQIndex) persistSnapshot(collection string) persistSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.data[collection]
	if !ok || !c.trained || len(c.codebooks) == 0 {
		return nil
	}
	return snapshotCodes(&trainedSnapshot{dim: c.dim, trainedOn: c.trainedOn,
		matrices: c.codebooks, codeWidth: len(c.codebooks)}, c.codes, c.origVecs)
}

// persistRestore implements Persistable.
func (p *PQIndex) persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error) {
	s, ok := section.(*trainedSection)
	if !ok {
		return nil, corruptf("the pq section is not a trained one")
	}
	if err := s.codebooksOf(s.matrices, p.nSubspaces, p.codebookSize); err != nil {
		return nil, err
	}
	codes, vecs, err := s.codesOf(vectors)
	if err != nil {
		return nil, err
	}
	c := &pqCollection{codebooks: s.matrices, codes: codes, origVecs: vecs, dim: s.dim, trainState: s.restored()}
	p.mu.Lock()
	p.data[collection] = c
	p.mu.Unlock()
	return missingFrom(vectors, vecs), nil
}

// PersistStamp implements Persistable.
func (o *OPQIndex) PersistStamp(collection string) uint64 {
	return stampUnder(&o.mu, func() *trainState {
		if c, ok := o.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

func (o *OPQIndex) persistKind() byte { return persistKindTrained }

// persistSnapshot implements Persistable.
func (o *OPQIndex) persistSnapshot(collection string) persistSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	c, ok := o.data[collection]
	if !ok || !c.trained || len(c.codebooks) == 0 {
		return nil
	}
	rotation := make([][]float32, c.dim)
	for i := range rotation {
		rotation[i] = c.rotation[i*c.dim : (i+1)*c.dim]
	}
	matrices := append([][][]float32{rotation}, c.codebooks...)
	return snapshotCodes(&trainedSnapshot{dim: c.dim, trainedOn: c.trainedOn,
		matrices: matrices, codeWidth: len(c.codebooks)}, c.codes, c.origVecs)
}

// persistRestore implements Persistable.
func (o *OPQIndex) persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error) {
	s, ok := section.(*trainedSection)
	if !ok || len(s.matrices) < 2 {
		return nil, corruptf("the opq section is not a trained one")
	}
	rows := s.matrices[0]
	if len(rows) != s.dim || len(rows[0]) != s.dim {
		return nil, corruptf("rotation of %d rows of %d for dimension %d", len(rows), len(rows[0]), s.dim)
	}
	if err := s.codebooksOf(s.matrices[1:], o.nSubspaces, o.codebookSize); err != nil {
		return nil, err
	}
	codes, vecs, err := s.codesOf(vectors)
	if err != nil {
		return nil, err
	}
	rotation := make([]float32, 0, s.dim*s.dim)
	for _, row := range rows {
		rotation = append(rotation, row...)
	}
	c := &opqCollection{rotation: rotation, codebooks: s.matrices[1:], codes: codes, origVecs: vecs,
		dim: s.dim, trainState: s.restored()}
	o.mu.Lock()
	o.data[collection] = c
	o.mu.Unlock()
	return missingFrom(vectors, vecs), nil
}

// --- SQ and SQ4: per-dimension parameters, one row each. The entries carry
// no code: they are there to tell whether the scales still describe the
// stored vectors, and the codes are computed again, which is a multiply and
// a clamp per dimension ---

// snapshotIDs captures which vectors a scalar-quantized collection coded.
func snapshotIDs[C any](s *trainedSnapshot, codes map[string]C, vecs map[string][]float32) *trainedSnapshot {
	for id := range codes {
		s.add(id, vecs[id])
	}
	return s
}

// scalarVectors is the stored vectors of the dimension the scales were
// trained for — the ones a scalar index codes again at load — and the rest,
// which the caller adds the usual way so Add can refuse and count them.
func scalarVectors(vectors map[string][]float32, dim int) (map[string][]float32, []string) {
	out := make(map[string][]float32, len(vectors))
	for id, v := range vectors {
		if len(v) == dim {
			out[id] = v
		}
	}
	return out, missingFrom(vectors, out)
}

// PersistStamp implements Persistable.
func (s *SQIndex) PersistStamp(collection string) uint64 {
	return stampUnder(&s.mu, func() *trainState {
		if c, ok := s.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

func (s *SQIndex) persistKind() byte { return persistKindTrained }

// persistSnapshot implements Persistable. The matrices are the minimums,
// the maximums and the scales.
func (s *SQIndex) persistSnapshot(collection string) persistSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[collection]
	if !ok || !c.trained || len(c.scales) == 0 {
		return nil
	}
	return snapshotIDs(&trainedSnapshot{dim: c.dim, trainedOn: c.trainedOn,
		matrices: [][][]float32{{c.mins}, {c.maxs}, {c.scales}}}, c.codes, c.origVecs)
}

// persistRestore implements Persistable.
func (s *SQIndex) persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error) {
	sec, err := trainedSectionOf(section, 3, 0)
	if err != nil {
		return nil, err
	}
	rows, err := sec.rowsOf(3)
	if err != nil {
		return nil, err
	}
	if _, err := sec.match(vectors); err != nil {
		return nil, err
	}
	vecs, missing := scalarVectors(vectors, sec.dim)
	c := &sqCollection{mins: rows[0], maxs: rows[1], scales: rows[2], dim: sec.dim,
		codes: make(map[string][]uint8, len(vecs)), origVecs: vecs, trainState: sec.restored()}
	for id, v := range vecs {
		c.codes[id] = s.encode(c, v)
	}
	s.mu.Lock()
	s.data[collection] = c
	s.mu.Unlock()
	return missing, nil
}

// PersistStamp implements Persistable.
func (s *SQ4Index) PersistStamp(collection string) uint64 {
	return stampUnder(&s.mu, func() *trainState {
		if c, ok := s.data[collection]; ok {
			return &c.trainState
		}
		return nil
	})
}

func (s *SQ4Index) persistKind() byte { return persistKindTrained }

// persistSnapshot implements Persistable. The matrices are the lower bounds
// and the scales; the levels follow from them.
func (s *SQ4Index) persistSnapshot(collection string) persistSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.data[collection]
	if !ok || !c.trained || len(c.scales) == 0 {
		return nil
	}
	return snapshotIDs(&trainedSnapshot{dim: c.dim, trainedOn: c.trainedOn,
		matrices: [][][]float32{{c.lows}, {c.scales}}}, c.codes, c.origVecs)
}

// persistRestore implements Persistable.
func (s *SQ4Index) persistRestore(collection string, section any, vectors map[string][]float32) ([]string, error) {
	sec, err := trainedSectionOf(section, 2, 0)
	if err != nil {
		return nil, err
	}
	rows, err := sec.rowsOf(2)
	if err != nil {
		return nil, err
	}
	if _, err := sec.match(vectors); err != nil {
		return nil, err
	}
	vecs, missing := scalarVectors(vectors, sec.dim)
	c := &sq4Collection{lows: rows[0], scales: rows[1], levels: make([][16]float32, sec.dim), dim: sec.dim,
		codes: make(map[string][]uint8, len(vecs)), origVecs: vecs, trainState: sec.restored()}
	for d := range c.levels {
		c.levels[d] = sq4LevelsOf(c.lows[d], c.scales[d])
	}
	for id, v := range vecs {
		c.codes[id] = c.encode(v)
	}
	s.mu.Lock()
	s.data[collection] = c
	s.mu.Unlock()
	return missing, nil
}
