package geo

// PointIndex is an in-memory index of points: GeoIndex and GeoHashIndex.
type PointIndex interface {
	Add(collection, docID string, lat, lng float64)
	Remove(collection, docID string)
}

// ApplyReplicated brings indexes up to one replicated change of the geo
// bucket — put is true for a Put, false for a Delete. A follower writes the
// bucket from the binlog, and its indexes used to be rebuilt from it only
// at start: a point added on the leader was not found by a follower's geo
// search until a restart or POST /v1/geo-reindex. A key or value it cannot
// read changes nothing.
func ApplyReplicated(put bool, key, value []byte, indexes ...PointIndex) {
	collection, docID, ok := parseGeoKey(key)
	if !ok {
		return
	}
	lat, lng, valid := decodeGeoValue(value)
	for _, idx := range indexes {
		switch {
		case !put:
			idx.Remove(collection, docID)
		case valid:
			idx.Add(collection, docID, lat, lng)
		}
	}
}
