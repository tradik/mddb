package vector

import "bytes"

// Image vectors (RAG-010).
//
// An image document's picture, embedded by a multimodal provider, is one
// vector of its own in the vectors bucket — so it moves with the collection
// in the per-collection layout, replicates and is backed up like every other
// vector — under the prefix "vimg|", which nothing that walks chunk vectors
// ("vec|") or summary vectors ("vsum|") sees. The record's ContentHash is the
// hash of the image bytes and its Model the multimodal model, so an unchanged
// image is not embedded again.

// ImageKeyPrefix starts every image vector key: vimg|collection|docID.
const ImageKeyPrefix = "vimg|"

// ImageKey builds the key of an image document's vector.
func ImageKey(collection, docID string) []byte {
	return []byte(ImageKeyPrefix + collection + "|" + docID)
}

// IsImageKey reports whether a vectors-bucket key holds an image vector.
func IsImageKey(key []byte) bool {
	return bytes.HasPrefix(key, []byte(ImageKeyPrefix))
}

// PutImage stores an image document's vector; imageHash is the hash of the
// image bytes.
func (vs *VectorStore) PutImage(collection, docID string, vector []float32, model, imageHash string) error {
	return vs.putDocVector(ImageKey(collection, docID), collection, docID, vector, model, imageHash)
}

// GetImage returns an image document's vector record, nil when it has none.
func (vs *VectorStore) GetImage(collection, docID string) *EmbeddingRecord {
	return vs.getDocVector(collection, ImageKey(collection, docID))
}

// DeleteImage removes an image document's vector, if it has one.
func (vs *VectorStore) DeleteImage(collection, docID string) error {
	return vs.deleteDocVector(collection, ImageKey(collection, docID))
}

// LoadImages returns a collection's image vectors keyed by document ID.
func (vs *VectorStore) LoadImages(collection string) map[string][]float32 {
	return vs.loadDocVectors(collection, ImageKey(collection, ""))
}

// ImageVectors reads the image vectors of the given documents in one read
// transaction — the disk half of a disk-only collection's image search.
func (vs *VectorStore) ImageVectors(collection string, docIDs []string) map[string][]float32 {
	return vs.docVectors(collection, docIDs, func(id string) []byte { return ImageKey(collection, id) })
}
